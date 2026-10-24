#!/usr/bin/env python3
"""Isolated Docker integration test. Requires Docker, Python 3 and a Linux daemon.

Builds test-only versions, uses an internal network and new labelled volumes,
and removes only its own Docker resources. No production settings are read.
Run from the checkout: python3 scripts/test-container-update.py
"""
import argparse
import http.cookiejar
import json
from pathlib import Path
import secrets
import subprocess
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--image", default="easyrun:container-update-qa")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    name = "easyrun-update-test-" + secrets.token_hex(5)
    out = root / ".local" / name
    out.mkdir(parents=True)
    log = (out / "build.log").open("w")
    label = "io.easyrun.container-test=" + name
    containers, volumes, images = [], [], []
    network = name + "-network"
    db, app = name + "-db", name + "-app"
    password = secrets.token_urlsafe(24)
    admin_password = secrets.token_urlsafe(24)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    base = ""

    def docker(*cmd, check=True, build=False):
        result = subprocess.run(["docker", *map(str, cmd)], cwd=root, text=True,
                                stdout=log if build else subprocess.PIPE,
                                stderr=log if build else subprocess.PIPE)
        if check and result.returncode:
            raise RuntimeError("Docker operation failed: " + str(cmd[0]) + "\n" +
                               ("See " + str(out / "build.log") if build else result.stderr))
        return result

    def wait(message, fn, seconds=100):
        deadline, last = time.monotonic() + seconds, None
        while time.monotonic() < deadline:
            try:
                result = fn()
                if result:
                    return result
            except Exception as exc:
                last = exc
            time.sleep(0.3)
        raise RuntimeError(message + (": " + str(last) if last else ""))

    def request(path, body=None):
        req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json"})
        with opener.open(req, timeout=3) as response:
            return json.load(response)

    def health(version):
        value = request("/healthz")
        return value if value.get("status") == "ok" and value["build"]["version"] == version else None

    def login():
        request("/api/v1/portal/auth/login", {"email": "container-test@example.invalid", "password": admin_password})

    def phase():
        result = docker("exec", app, "cat", "/app/runtime/.easyrun-update/state.json", check=False)
        return json.loads(result.stdout).get("phase") if result.returncode == 0 else ""

    def protected_digest():
        sql = """SELECT md5(
          (SELECT config::text||secrets::text FROM portal_settings WHERE id=1)||
          COALESCE((SELECT string_agg(row_to_json(u)::text,',' ORDER BY u.id) FROM portal_users u),'[]')||
          COALESCE((SELECT string_agg(row_to_json(s)::text,',' ORDER BY s.singleton_id) FROM system_settings s),'[]'))"""
        db_hash = docker("exec", db, "psql", "-U", "fixture", "-d", "fixture", "-Atc", sql).stdout.strip()
        key_hash = docker("exec", app, "sha256sum", "/app/data/account-master.key").stdout.split()[0]
        assert len(db_hash) == 32 and len(key_hash) == 64, "Protected-data digest missing"
        return db_hash, key_hash

    def stop_app():
        docker("stop", "-t", "30", app)
        details = json.loads(docker("inspect", app).stdout)[0]
        assert details["State"]["ExitCode"] == 0, "Container did not stop cleanly"
        docker("rm", app)

    def start_app(image, expected):
        nonlocal base
        docker("run", "-d", "--init", "--name", app, "--label", label, "--network", network,
               "-e", "DATABASE_URL=postgres://fixture:" + password + "@" + db + ":5432/fixture?sslmode=disable",
               "-e", "EASYRUN_DATA_DIR=/app/data", "-e", "PROTOCOL_BASE_URL=http://127.0.0.1:1/run-front",
               "-e", "PROTOCOL_TIMEOUT=1s", "-e", "EASYRUN_ISOLATED_CONTAINER_TEST=1",
               "-v", name + "-data:/app/data", "-v", name + "-runtime:/app/runtime", image)
        if app not in containers:
            containers.append(app)
        details = json.loads(docker("inspect", app).stdout)[0]
        # The Linux host can access its internal bridge directly. Recent Docker
        # versions intentionally do not publish ports from internal networks.
        ip = details["NetworkSettings"]["Networks"][network]["IPAddress"]
        if not ip or not details["State"]["Running"]:
            raise RuntimeError("Fixture container failed to start; see build.log")
        base = "http://" + ip + ":8080"
        if details["HostConfig"]["Privileged"] or any("docker.sock" in str(m) for m in details["Mounts"]):
            raise RuntimeError("Unexpected privilege or Docker socket mount")
        if docker("exec", app, "id", "-u").stdout.strip() != "10001":
            raise RuntimeError("Application is not running as UID 10001")
        result = wait("container did not become healthy", lambda: health(expected))
        if not result.get("face_runtime_ready") or result["build"].get("container_runtime") != 1:
            raise RuntimeError("Native inference or container runtime metadata missing")
        for file in ("container-driver", "server-next", "server-incompatible", "server-bad"):
            docker("cp", out / file, app + ":/tmp/" + file)

    def stage(file, version, reject=False):
        result = docker("exec", app, "/tmp/container-driver", "/tmp/" + file, version, check=False)
        if reject:
            if result.returncode != 2 or "守护协议不兼容" not in result.stdout:
                raise RuntimeError("Incompatible candidate was not rejected: " + result.stdout + result.stderr)
        elif result.returncode or not json.loads(result.stdout).get("prepared"):
            raise RuntimeError("Fixture updater failed: " + result.stdout + result.stderr)

    try:
        build_args = ["--build-arg", "VERSION=0.1.4", "--build-arg", "COMMIT=container-update-test",
                      "--build-arg", "BUILT_AT=2026-10-24T00:00:00Z"]
        print("Building isolated container fixtures...", flush=True)
        docker("build", *build_args, "-t", args.image, ".", build=True)
        builder = name + "-builder"
        docker("build", *build_args, "--target", "build", "--label", label, "-t", builder, ".", build=True)
        images.append(builder)
        pkg = "github.com/Zolohacker/EasyRun/internal/buildinfo"
        commands = [
            "go test -count=1 ./internal/selfupdate",
            "go build -buildvcs=false -o /out/container-driver ./internal/selfupdate/testdata/container-driver",
            f'go build -buildvcs=false -trimpath -ldflags="-s -w -X {pkg}.Version=0.1.5 -X {pkg}.Commit=container-next -X {pkg}.BuiltAt=fixture -X {pkg}.BuildType=release" -o /out/server-next ./cmd/server',
            f'go build -buildvcs=false -trimpath -ldflags="-s -w -X {pkg}.Version=0.1.6 -X {pkg}.Commit=container-new-image -X {pkg}.BuiltAt=fixture -X {pkg}.BuildType=container" -o /out/server-image ./cmd/server',
            'go build -buildvcs=false -ldflags="-X main.version=0.1.6 -X main.protocol=0" -o /out/server-incompatible ./internal/selfupdate/testdata/container-driver/bad',
            'go build -buildvcs=false -ldflags="-X main.version=0.1.7 -X main.protocol=1" -o /out/server-bad ./internal/selfupdate/testdata/container-driver/bad',
        ]
        docker("run", "--rm", "--network", "none", "--label", label, "-e", "CGO_ENABLED=1",
               "-v", str(root) + ":/src:ro", "-v", str(out) + ":/out", "-w", "/src", builder,
               "sh", "-ec", " && ".join(commands), build=True)
        # No production addresses are reachable from this test network.
        docker("network", "create", "--internal", "--label", label, network)
        for suffix in ("db", "data", "runtime"):
            volume = name + "-" + suffix
            docker("volume", "create", "--label", label, volume)
            volumes.append(volume)
        docker("run", "-d", "--name", db, "--label", label, "--network", network,
               "-e", "POSTGRES_USER=fixture", "-e", "POSTGRES_DB=fixture", "-e", "POSTGRES_PASSWORD=" + password,
               "-v", name + "-db:/var/lib/postgresql/data", "postgres:17-alpine")
        containers.append(db)
        wait("fixture database did not start", lambda: docker("exec", db, "pg_isready", "-h", "127.0.0.1", "-U", "fixture", check=False).returncode == 0)
        start_app(args.image, "0.1.4")
        request("/api/v1/portal/setup", {"email": "container-test@example.invalid", "password": admin_password, "mode": "personal"})
        login()
        status = request("/api/v1/portal/system/versions")["platform"]
        assert status["deployment_mode"] == "container_managed" and not status["capability_reason"], status
        original = protected_digest()
        print("PASS: non-root bootstrap, writable named volume and managed update status", flush=True)

        stage("server-next", "0.1.5")
        wait("live update failed", lambda: phase() == "succeeded" and health("0.1.5"))
        assert protected_digest() == original, "Live update changed protected data"
        print("PASS: real download/checksum/prepare/install/health-verification pipeline", flush=True)

        stop_app()
        start_app(args.image, "0.1.5")
        assert protected_digest() == original, "Recreating the container changed protected data"
        login()
        assert request("/api/v1/portal/system/versions")["platform"]["deployment_mode"] == "container_managed"
        print("PASS: recreated container retains the online-installed version and user data", flush=True)

        before_boot = health("0.1.5")["boot_id"]
        stage("server-incompatible", "0.1.6", reject=True)
        assert health("0.1.5")["boot_id"] == before_boot, "Incompatible candidate restarted the service"
        print("PASS: incompatible container protocol rejected before replacement", flush=True)

        stage("server-bad", "0.1.7")
        wait("bad release was not rolled back", lambda: phase() == "rolled_back" and health("0.1.5"))
        assert protected_digest() == original, "Rollback changed protected data"
        print("PASS: broken release rolls back with database, wallet settings and master key unchanged", flush=True)

        image_dockerfile = out / "Dockerfile.image-switch"
        image_dockerfile.write_text("FROM " + args.image + "\nCOPY server-image /usr/local/bin/easyrun\n")
        new_image = name + "-new-image"
        docker("build", "--label", label, "-f", image_dockerfile, "-t", new_image, out, build=True)
        images.append(new_image)
        stop_app()
        start_app(new_image, "0.1.6")
        login()
        status = request("/api/v1/portal/system/versions")["platform"]
        assert not status["can_rollback"] and not status["phase"], "Previous image update remained active"
        assert protected_digest() == original, "Image deployment changed protected data"
        print("PASS: explicit new image takes precedence without cross-image rollback state", flush=True)

        result = {"result": "passed", "bootstrap": "non-root", "online_update": "0.1.4 -> 0.1.5",
                  "recreate_persistence": True, "incompatible_protocol_rejected": True,
                  "automatic_rollback": "0.1.7 -> 0.1.5", "image_switch": "0.1.6",
                  "database_wallet_master_key_unchanged": True, "docker_socket_mounted": False}
        (out / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps(result, ensure_ascii=False), flush=True)
        print("Artifacts: " + str(out), flush=True)
    finally:
        # Every name below was created by this invocation with a random prefix.
        for item in reversed(containers):
            if item.startswith(name + "-"):
                docker("logs", "--tail", "40", item, check=False, build=True)
                docker("rm", "-f", item, check=False)
        for item in volumes:
            if item.startswith(name + "-"):
                docker("volume", "rm", item, check=False)
        docker("network", "rm", network, check=False)
        for item in images:
            if item.startswith(name + "-"):
                docker("image", "rm", item, check=False)
        log.close()


if __name__ == "__main__":
    main()
