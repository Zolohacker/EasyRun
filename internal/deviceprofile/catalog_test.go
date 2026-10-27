package deviceprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

var verifiedDate = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func TestCatalogHasEvidenceForEveryModelReleaseAndAndroidVersion(t *testing.T) {
	var data struct {
		VerifiedAt string `json:"verified_at"`
		Sources    map[string]struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"sources"`
		Devices []struct {
			Profile
			Evidence struct {
				Model   []string            `json:"model"`
				Release []string            `json:"release"`
				Android map[string][]string `json:"android"`
			} `json:"evidence"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(catalogJSON, &data); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse("2006-01-02", data.VerifiedAt); err != nil {
		t.Fatal(err)
	}
	seenIDs, seenModels, brands := map[string]bool{}, map[string]bool{}, map[string]int{}
	for _, device := range data.Devices {
		if device.ID == "" || device.Name == "" || device.CertifiedName == "" || seenIDs[device.ID] || seenModels[device.ModelName] {
			t.Fatalf("missing or duplicate catalog identity: %s", device.ID)
		}
		seenIDs[device.ID], seenModels[device.ModelName] = true, true
		brands[device.Brand]++
		if brand, model, ok := strings.Cut(device.ModelName, "|"); !ok || brand == "" || model == "" || strings.Contains(model, "|") {
			t.Fatalf("invalid wire model: %s", device.ID)
		}
		if _, err := time.Parse("2006-01", device.ReleaseMonth); err != nil {
			t.Fatalf("invalid release month: %s", device.ID)
		}
		checkSources := func(ids []string) {
			t.Helper()
			if len(ids) == 0 {
				t.Fatalf("missing evidence: %s", device.ID)
			}
			for _, id := range ids {
				source, ok := data.Sources[id]
				u, err := url.Parse(source.URL)
				if !ok || err != nil || u.Scheme != "https" || u.Host == "" || source.Title == "" {
					t.Fatalf("invalid evidence %s for %s", id, device.ID)
				}
			}
		}
		checkSources(device.Evidence.Model)
		checkSources(device.Evidence.Release)
		if len(device.AndroidVersions) == 0 || len(device.Evidence.Android) != len(device.AndroidVersions) {
			t.Fatalf("missing Android evidence: %s", device.ID)
		}
		versions := map[string]bool{}
		for _, version := range device.AndroidVersions {
			if !slices.Contains([]string{"13", "14", "15", "16"}, version) || versions[version] {
				t.Fatalf("invalid or duplicate Android major: %s", device.ID)
			}
			versions[version] = true
			checkSources(device.Evidence.Android[version])
		}
	}
	if len(brands) != 7 || len(data.Devices) != 28 {
		t.Fatalf("unexpected catalog coverage: brands=%d models=%d", len(brands), len(data.Devices))
	}
	for _, brand := range []string{"小米", "红米", "三星", "OPPO", "vivo", "一加", "iQOO"} {
		if brands[brand] != 4 {
			t.Fatalf("unexpected model count for %s: %d", brand, brands[brand])
		}
	}
}

func TestRecentUsesRollingFourYearsAndOmitsFutureDevices(t *testing.T) {
	if got := len(Recent(verifiedDate)); got != 28 {
		t.Fatalf("current pool has %d devices", got)
	}
	for _, profile := range Recent(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		if profile.ReleaseMonth > "2025-01" {
			t.Fatalf("included future model %s", profile.ID)
		}
	}
	for _, profile := range Recent(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		if profile.ID == "vivo-x90-cn" {
			t.Fatal("month-only release evidence must age out conservatively")
		}
	}
	if len(Recent(time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC))) != 0 {
		t.Fatal("expired catalog kept old devices")
	}
	first := Recent(verifiedDate)
	first[0].AndroidVersions[0] = "invalid"
	if Recent(verifiedDate)[0].AndroidVersions[0] == "invalid" {
		t.Fatal("caller mutated the embedded catalog")
	}
}

func TestEveryRandomChoiceUsesOneVerifiedPair(t *testing.T) {
	profiles := Recent(verifiedDate)
	for i, want := range profiles {
		model, version, err := complete("", "", verifiedDate, bytes.NewReader([]byte{byte(i), 0}))
		if err != nil || model != want.ModelName || !slices.Contains(want.AndroidVersions, version) {
			t.Fatalf("choice %d: model=%s version=%s err=%v", i, model, version, err)
		}
		model, version, err = complete(want.ModelName, "", verifiedDate, bytes.NewReader([]byte{0}))
		if err != nil || model != want.ModelName || !slices.Contains(want.AndroidVersions, version) {
			t.Fatalf("partial model %s: %s %s %v", want.ID, model, version, err)
		}
	}
}

type brokenRandom struct{}

func (brokenRandom) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestExplicitAndLegacyPairsNeverRandomize(t *testing.T) {
	for _, pair := range [][2]string{{"Redmi|23013RK75C", "14"}, {"Custom|REAL_DEVICE", "17"}, {"vivo|V2307A", "14"}} {
		model, version, err := complete(" "+pair[0]+" ", " "+pair[1]+" ", verifiedDate.AddDate(10, 0, 0), brokenRandom{})
		if err != nil || model != pair[0] || version != pair[1] {
			t.Fatalf("changed explicit pair: %v %s %s %v", pair, model, version, err)
		}
	}
}

func TestPartialVersionNeverSelectsAnIncompatibleModel(t *testing.T) {
	model, version, err := complete("", "16", verifiedDate, brokenRandom{})
	if err != nil || model != "samsung|SM-S9420" || version != "16" {
		t.Fatalf("partial version: %s %s %v", model, version, err)
	}
	if _, _, err := complete("Custom|REAL_DEVICE", "", verifiedDate, brokenRandom{}); err == nil {
		t.Fatal("attached an arbitrary Android version to an unknown model")
	}
	if _, _, err := complete("", "999", verifiedDate, brokenRandom{}); err == nil {
		t.Fatal("accepted an unsupported version without an explicit model")
	}
	if _, _, err := complete("", "", verifiedDate, brokenRandom{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("random source failure was hidden: %v", err)
	}
	if _, _, err := Complete("", "", verifiedDate.AddDate(10, 0, 0)); err == nil {
		t.Fatal("silently used expired catalog")
	}
}
