package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckAppUpdate(t *testing.T) {
	var response any = map[string]any{"newVersion": "8.8.0", "supportVersion": "8.8.0", "haveUpdate": true, "forceUpdate": true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/common/update" || r.Method != "POST" || r.Header.Get("Authorization") != "" || r.Header.Get("satoken") != "" {
			t.Error("unexpected update request or account credential used")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["appVersion"] != AppVersion || body["buildVersion"] != BuildVersion || r.Header.Get("sign") == "" {
			t.Error("missing actual client baseline/signature")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": response})
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL})
	result, err := client.CheckAppUpdate(context.Background(), Device{ID: "fixture-only", ModelName: "Redmi|23013RK75C", SystemVersion: "14"})
	if err != nil || !result.ForceUpdate || result.NewVersion != "8.8.0" {
		t.Fatal(result, err)
	}
	response = map[string]any{"haveUpdate": false} // Actual no-update response omits forceUpdate.
	result, err = client.CheckAppUpdate(context.Background(), Device{})
	if err != nil || result.NewVersion != AppVersion || result.ForceUpdate {
		t.Fatal(result, err)
	}
	for _, invalid := range []any{nil, map[string]any{}, map[string]any{"haveUpdate": nil}, map[string]any{"haveUpdate": false, "forceUpdate": nil}, map[string]any{"haveUpdate": true}, map[string]any{"haveUpdate": true, "forceUpdate": true}} {
		response = invalid
		if _, err := client.CheckAppUpdate(context.Background(), Device{}); err == nil {
			t.Fatal("invalid/missing flags accepted", invalid)
		}
	}
}
