#!/usr/bin/env sh
# Verify bundled files; no APK extraction or model download is required.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../models" && pwd)
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) printf 'Supported Linux architectures: x86_64, aarch64\n' >&2; exit 2 ;;
esac
if [ "$#" -gt 0 ]; then root=$(realpath "$1"); fi
cd "$root"
grep -E "  (Megatron|linux-${arch}/libInspireFace.so)$" SHA256SUMS | sha256sum -c -
library="$root/linux-$arch/libInspireFace.so"
if ldd "$library" | grep -q 'not found'; then
  ldd "$library" >&2
  printf 'Install the missing C++/OpenMP runtime dependencies.\n' >&2
  exit 2
fi
printf 'Bundled models verified; start EasyRun normally, with CGO enabled.\n'
