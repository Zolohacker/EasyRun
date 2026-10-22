package config

import "testing"

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
