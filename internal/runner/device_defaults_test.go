package runner

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/deviceprofile"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
)

func TestOnboardingDeviceIsCompleteAndSurvivesTheSMSDraft(t *testing.T) {
	s := New(nil, nil, nil, nil)
	account, err := s.prepareOnboardingAccount(platform.Account{LoginName: "device-fixture@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(account.DeviceID) != 16 {
		t.Fatal("missing generated Android ID")
	}
	matched := false
	for _, profile := range deviceprofile.Recent(time.Now()) {
		matched = matched || account.ModelName == profile.ModelName && slices.Contains(profile.AndroidVersions, account.SystemVersion)
	}
	if !matched {
		t.Fatalf("unknown default pair %s / %s", account.ModelName, account.SystemVersion)
	}
	for i := 0; i < 5; i++ {
		resumed, err := s.prepareOnboardingAccount(account)
		if err != nil || resumed.DeviceID != account.DeviceID || resumed.ModelName != account.ModelName || resumed.SystemVersion != account.SystemVersion {
			t.Fatalf("SMS draft changed identity: %v", err)
		}
		account = resumed
	}
}

type deviceAccountStore struct {
	store.Store
	account platform.Account
}

func (s *deviceAccountStore) GetAccount(context.Context, string) (platform.Account, error) {
	return s.account, nil
}

func (s *deviceAccountStore) UpdateAccount(_ context.Context, account platform.Account) (platform.Account, error) {
	s.account = account
	return account, nil
}

func TestMetadataUpdateKeepsSavedDeviceAndAndroidID(t *testing.T) {
	existing := platform.Account{ID: "acct-fixture", LoginName: "device-fixture@example.test", DeviceID: "1234567890abcdef", ModelName: "Redmi|23013RK75C", SystemVersion: "14"}
	database := &deviceAccountStore{account: existing}
	s := New(database, nil, nil, nil)
	updated, err := s.UpdateAccount(context.Background(), platform.Account{ID: existing.ID, LoginName: existing.LoginName, Note: "updated"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DeviceID != existing.DeviceID || updated.ModelName != existing.ModelName || updated.SystemVersion != existing.SystemVersion {
		t.Fatal("metadata-only edit randomized an existing device")
	}
}

func TestExplicitManualDeviceIsKept(t *testing.T) {
	s := New(nil, nil, nil, nil)
	account, err := s.prepareOnboardingAccount(platform.Account{LoginName: "fixture@example.test", DeviceID: "1234567890abcdef", ModelName: "Custom|EXPLICIT", SystemVersion: "17"})
	if err != nil || account.DeviceID != "1234567890abcdef" || account.ModelName != "Custom|EXPLICIT" || account.SystemVersion != "17" {
		t.Fatalf("manual device overwritten: %v", err)
	}
}
