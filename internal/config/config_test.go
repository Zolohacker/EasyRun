package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAutomationRunTimeoutDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		unset   bool
		want    time.Duration
		wantErr bool
	}{
		{name: "unset", unset: true, want: 90 * time.Minute},
		{name: "empty", want: 90 * time.Minute},
		{name: "blank", value: "  ", want: 90 * time.Minute},
		{name: "documented default", value: "90m", want: 90 * time.Minute},
		{name: "custom longer timeout", value: "2h", want: 2 * time.Hour},
		{name: "explicit shorter timeout", value: "3m", want: 3 * time.Minute},
		{name: "minimum", value: "10s", want: 10 * time.Second},
		{name: "below minimum", value: "9s", wantErr: true},
		{name: "zero", value: "0s", wantErr: true},
		{name: "negative", value: "-1m", wantErr: true},
		{name: "invalid duration", value: "invalid", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTOMATION_RUN_TIMEOUT", tc.value)
			if tc.unset {
				if err := os.Unsetenv("AUTOMATION_RUN_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "AUTOMATION_RUN_TIMEOUT") {
					t.Fatalf("expected timeout validation error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AutomationRunTimeout != tc.want {
				t.Fatalf("timeout=%s want=%s", cfg.AutomationRunTimeout, tc.want)
			}
		})
	}
}

func TestProtocolDefaultsMatchObservedProRuntime(t *testing.T) {
	t.Setenv("PROTOCOL_BASE_URL", "")
	t.Setenv("PROTOCOL_ENVIRONMENT", "")

	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.ProtocolEnvironment != 9 {
		t.Fatalf("protocol environment=%d want=9", configuration.ProtocolEnvironment)
	}
	if configuration.ProtocolBaseURL != "https://api.huachenjie.com/run-front" {
		t.Fatalf("protocol base URL=%q", configuration.ProtocolBaseURL)
	}
}

func TestMapKeysAreNeverReadFromEnvironment(t *testing.T) {
	t.Setenv("MAP_API_KEY", "obsolete-environment-key")
	t.Setenv("MAP_API_BASE_URL", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MapConfigured() || c.MapBaseURL != "https://restapi.amap.com" {
		t.Fatal("unexpected environment map key configuration")
	}
}
func TestBundledFaceAssetsAreDiscoveredWithoutEnvironmentPaths(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	defer os.Chdir(old)
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACE_ENGINE_LIBRARY_PATH", "")
	t.Setenv("FACE_MODEL_PACK_PATH", "")
	root := filepath.Join(dir, "models")
	native := filepath.Join(root, runtime.GOOS+"-"+runtime.GOARCH)
	_ = os.MkdirAll(native, 0755)
	lib := "libInspireFace.so"
	if runtime.GOOS == "windows" {
		lib = "libInspireFace.dll"
	}
	_ = os.WriteFile(filepath.Join(root, "Megatron"), []byte("fixture"), 0644)
	_ = os.WriteFile(filepath.Join(native, lib), []byte("fixture"), 0644)
	c, err := Load()
	if err != nil || !c.FaceEngineConfigured() {
		t.Fatalf("bundled model not detected: %v", err)
	}
}

func TestMapAPIDefaultQPSIsThree(t *testing.T) {
	t.Setenv("MAP_API_QPS", "")

	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.MapQPS != 3 {
		t.Fatalf("map API QPS=%v want=3", configuration.MapQPS)
	}
}
