package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckAppUpdate(t *testing.T) {
	var response any = map[string]any{"newVersion": "9.0.0", "supportVersion": "8.8.1", "haveUpdate": true, "forceUpdate": true}
	marketVersion := AppVersion
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/common/update":
			if r.Method != "POST" || r.Header.Get("Authorization") != "" || r.Header.Get("satoken") != "" {
				t.Error("unexpected update request or account credential used")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appVersion"] != AppVersion || body["buildVersion"] != BuildVersion || r.Header.Get("sign") == "" {
				t.Error("missing actual client baseline/signature")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": response})
		case "/market":
			if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("satoken") != "" {
				t.Error("unexpected market request or account credential used")
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, appMarketFixture(appMarketPackage, marketVersion))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL, AppMarketURL: server.URL + "/market"})
	result, err := client.CheckAppUpdate(context.Background(), Device{ID: "fixture-only", ModelName: "Redmi|23013RK75C", SystemVersion: "14"})
	if err != nil || !result.ForceUpdate || result.NewVersion != "9.0.0" || result.MarketVersion != AppVersion {
		t.Fatal(result, err)
	}
	marketVersion = "9.1.0"
	result, err = client.CheckAppUpdate(context.Background(), Device{})
	if err != nil || !result.ForceUpdate || result.NewVersion != "9.0.0" || result.MarketVersion != marketVersion {
		t.Fatal("market version replaced mandatory client target", result, err)
	}
	marketVersion = AppVersion
	response = map[string]any{"haveUpdate": false} // Actual no-update response omits forceUpdate.
	result, err = client.CheckAppUpdate(context.Background(), Device{})
	if err != nil || result.NewVersion != AppVersion || result.HaveUpdate || result.ForceUpdate || result.VersionSource != "client_update_api+app_market" {
		t.Fatal(result, err)
	}
	marketVersion = "8.9.0"
	result, err = client.CheckAppUpdate(context.Background(), Device{})
	if err != nil || result.NewVersion != marketVersion || !result.HaveUpdate || result.ForceUpdate || result.MarketURL == "" || !strings.Contains(result.UpdateTitle, marketVersion) {
		t.Fatal(result, err)
	}
	marketVersion = AppVersion
	for _, invalid := range []any{nil, map[string]any{}, map[string]any{"haveUpdate": nil}, map[string]any{"haveUpdate": false, "forceUpdate": nil}, map[string]any{"haveUpdate": true}, map[string]any{"haveUpdate": true, "forceUpdate": true}} {
		response = invalid
		if _, err := client.CheckAppUpdate(context.Background(), Device{}); err == nil {
			t.Fatal("invalid/missing flags accepted", invalid)
		}
	}
}

func TestCheckAppUpdateKeepsClientAPIResultWhenMarketFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/common/update" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"haveUpdate": false}})
			return
		}
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL, AppMarketURL: server.URL + "/market"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CheckAppUpdate(context.Background(), Device{})
	if err != nil || result.NewVersion != AppVersion || result.VersionSource != "client_update_api" || result.MarketError == "" {
		t.Fatal(result, err)
	}
}

func TestParseAppMarketPageAndCompareVersions(t *testing.T) {
	page := `<html><script id="__NEXT_DATA__" type="application/json">{"decoy":{"pkg_name":"other","version_name":"99.0.0"},"target":{"pkg_name":"` + appMarketPackage + `","version_name":"V8.8.1"}}</script></html>`
	record, err := parseAppMarketPage([]byte(page), appMarketPackage)
	if err != nil || record.PackageName != appMarketPackage || record.Version != "8.8.1" {
		t.Fatal(record, err)
	}
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"8.8.1", "8.8.0", 1},
		{"8.8", "8.8.0", 0},
		{"8.8.0", "8.8.1", -1},
	} {
		got, err := compareAppVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Fatalf("compare %s %s = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
	if _, err := parseAppMarketPage([]byte(`<html></html>`), appMarketPackage); err == nil {
		t.Fatal("page without __NEXT_DATA__ accepted")
	}
	if _, _, err := parseAppVersion("8.8.1-beta"); err == nil {
		t.Fatal("non-numeric version accepted")
	}
}

func appMarketFixture(packageName, version string) string {
	document, _ := json.Marshal(map[string]any{
		"props": map[string]any{"pageProps": map[string]any{"dynamicCardResponse": map[string]any{
			"data": map[string]any{"components": []any{map[string]any{"data": map[string]any{"itemData": []any{
				map[string]any{"pkg_name": packageName, "version_name": version},
			}}}}},
		}}},
	})
	return `<html><script id="__NEXT_DATA__" type="application/json">` + string(document) + `</script></html>`
}
