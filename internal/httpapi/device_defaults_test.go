package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/platform"
)

func TestNewAccountValidationCompletesDeviceBeforeLogin(t *testing.T) {
	a := &api{}
	r := httptest.NewRequest("POST", "/api/v1/accounts/onboard/password", nil)
	account := platform.Account{LoginName: "device-fixture@example.test", SystemVersion: "16"}
	if err := a.validateAccount(r, &account); err != nil {
		t.Fatal(err)
	}
	if account.ModelName != "samsung|SM-S9420" || account.SystemVersion != "16" {
		t.Fatal("onboarding did not fill a matching model")
	}
}

func TestNewAccountValidationRejectsUnknownHalfPair(t *testing.T) {
	a := &api{}
	r := httptest.NewRequest("POST", "/api/v1/accounts/onboard/sms/request", nil)
	account := platform.Account{LoginName: "device-fixture@example.test", ModelName: "Custom|EXPLICIT"}
	if err := a.validateAccount(r, &account); err == nil {
		t.Fatal("arbitrary Android version added to a custom model")
	}
}

func TestUpdateValidationLeavesMissingDeviceForServiceToPreserve(t *testing.T) {
	a := &api{}
	r := httptest.NewRequest("PUT", "/api/v1/accounts/acct-fixture", nil)
	account := platform.Account{ID: "acct-fixture", LoginName: "device-fixture@example.test"}
	if err := a.validateAccount(r, &account); err != nil {
		t.Fatal(err)
	}
	if account.ModelName != "" || account.SystemVersion != "" {
		t.Fatal("update validation generated a replacement identity")
	}
}
