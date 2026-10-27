// Package deviceprofile supplies a shared, source-backed device/Android catalog.
// Selection happens once during onboarding, never on the request hot path.
package deviceprofile

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"time"
)

//go:embed catalog.json
var catalogJSON []byte

type Profile struct {
	ID              string   `json:"id"`
	Brand           string   `json:"brand"`
	Name            string   `json:"name"`
	CertifiedName   string   `json:"certified_name"`
	ModelName       string   `json:"model_name"`
	ReleaseMonth    string   `json:"release_month"`
	AndroidVersions []string `json:"android_versions"`
}

var catalog = func() struct {
	WindowYears int       `json:"window_years"`
	Devices     []Profile `json:"devices"`
} {
	var value struct {
		WindowYears int       `json:"window_years"`
		Devices     []Profile `json:"devices"`
	}
	if err := json.Unmarshal(catalogJSON, &value); err != nil || value.WindowYears != 4 || len(value.Devices) == 0 {
		panic("invalid embedded device catalog")
	}
	return value
}()

// Recent returns defensive copies of profiles released within the last four
// years. Month-only release evidence is conservatively treated as the first
// day of that month, rather than inventing a precise launch day.
func Recent(at time.Time) []Profile {
	date := at.UTC()
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	cutoff := date.AddDate(-catalog.WindowYears, 0, 0)
	profiles := make([]Profile, 0, len(catalog.Devices))
	for _, profile := range catalog.Devices {
		released, err := time.Parse("2006-01", profile.ReleaseMonth)
		if err != nil || released.Before(cutoff) || released.After(date) {
			continue
		}
		profile.AndroidVersions = slices.Clone(profile.AndroidVersions)
		profiles = append(profiles, profile)
	}
	return profiles
}

// Complete fills missing fields using a compatible, verified profile. Explicit
// pairs (including legacy accounts and manually entered real devices) are kept
// intact. An unknown model requires an explicit Android version: there is no
// unrelated default system version to attach to arbitrary models.
func Complete(modelName, systemVersion string, at time.Time) (string, string, error) {
	return complete(modelName, systemVersion, at, rand.Reader)
}

func complete(modelName, systemVersion string, at time.Time, random io.Reader) (string, string, error) {
	modelName, systemVersion = strings.TrimSpace(modelName), strings.TrimSpace(systemVersion)
	if modelName != "" && systemVersion != "" {
		return modelName, systemVersion, nil
	}
	var candidates []Profile
	for _, profile := range Recent(at) {
		if modelName != "" && !strings.EqualFold(profile.ModelName, modelName) {
			continue
		}
		if systemVersion != "" && !slices.Contains(profile.AndroidVersions, systemVersion) {
			continue
		}
		candidates = append(candidates, profile)
	}
	if len(candidates) == 0 {
		return "", "", errors.New("设备资料库没有匹配组合，请同时填写设备机型和 Android 版本")
	}
	index, err := randomIndex(random, len(candidates))
	if err != nil {
		return "", "", err
	}
	profile := candidates[index]
	if modelName == "" {
		modelName = profile.ModelName
	}
	if systemVersion == "" {
		index, err = randomIndex(random, len(profile.AndroidVersions))
		if err != nil {
			return "", "", err
		}
		systemVersion = profile.AndroidVersions[index]
	}
	return modelName, systemVersion, nil
}

func randomIndex(random io.Reader, length int) (int, error) {
	value, err := rand.Int(random, big.NewInt(int64(length)))
	if err != nil {
		return 0, fmt.Errorf("select device profile: %w", err)
	}
	return int(value.Int64()), nil
}
