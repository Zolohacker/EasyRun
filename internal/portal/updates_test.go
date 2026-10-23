package portal

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/safety"
	"github.com/Zolohacker/EasyRun/internal/selfupdate"
	"golang.org/x/crypto/bcrypt"
)

type updateChecker struct{ err error }

func (c *updateChecker) CheckAppUpdate(context.Context, protocol.Device) (protocol.AppUpdate, error) {
	return protocol.AppUpdate{HaveUpdate: true, ForceUpdate: true, NewVersion: "99.0.0"}, c.err
}
func TestPublicUpdateSettingsPermissionsAndPersistedMandatoryGate(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	checker := &updateChecker{}
	g, err := safety.New(ctx, db.Pool(), checker, "fixture-upstream", protocol.Device{})
	if err != nil {
		t.Fatal(err)
	}
	s.ClientVersions = g
	exe := filepath.Join(t.TempDir(), "easyrun.exe")
	if err = os.WriteFile(exe, []byte("fixture target"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(ctx, `CREATE TABLE platform_update_settings(id smallint PRIMARY KEY,auto_install boolean NOT NULL DEFAULT false,token_cipher bytea);INSERT INTO platform_update_settings(id,token_cipher)VALUES(1,'legacy-fixture-secret'::bytea)`)
	if err != nil {
		t.Fatal(err)
	}
	s.PlatformUpdates, err = selfupdate.New(ctx, selfupdate.Options{DB: db.Pool(), Guard: g, Executable: exe})
	if err != nil {
		t.Fatal(err)
	}
	server := portalTestServer(s)
	defer server.Close()
	depositScanSettings(t, s, "https://example.test")
	root := depositScanClient(t, s, server.URL, "root", "superadmin", "", 0)
	admin := depositScanClient(t, s, server.URL, "admin", "admin", "", 0)
	customer := depositScanClient(t, s, server.URL, "customer", "customer", "", 0)
	hash, _ := bcrypt.GenerateFromPassword([]byte("fixture-password"), bcrypt.MinCost)
	if _, err = s.db.Exec(ctx, `UPDATE portal_users SET password_hash=$1 WHERE id='root'`, hash); err != nil {
		t.Fatal(err)
	}
	requestTest(t, newClient(), server.URL, http.MethodGet, "/api/v1/portal/system/versions", nil, 401)
	requestTest(t, customer, server.URL, http.MethodGet, "/api/v1/portal/system/versions", nil, 403)
	requestTest(t, admin, server.URL, http.MethodPut, "/api/v1/portal/system/versions/settings", map[string]any{}, 403)
	requestTest(t, admin, server.URL, http.MethodPost, "/api/v1/portal/system/versions/platform/install", map[string]any{}, 403)
	requestTest(t, root, server.URL, http.MethodPut, "/api/v1/portal/system/versions/settings", map[string]any{"password": "wrong"}, 403)
	requestTest(t, root, server.URL, http.MethodPut, "/api/v1/portal/system/versions/settings", map[string]any{"password": "fixture-password", "token": "obsolete-field"}, 400)
	raw := requestTest(t, root, server.URL, http.MethodPut, "/api/v1/portal/system/versions/settings", map[string]any{"password": "fixture-password", "auto_install": false}, 200)
	if bytes.Contains(raw, []byte("token_configured")) || bytes.Contains(raw, []byte("legacy-fixture-secret")) {
		t.Fatal("obsolete credential settings exposed")
	}
	var cipher []byte
	if err = s.db.QueryRow(ctx, `SELECT token_cipher FROM platform_update_settings WHERE id=1`).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if len(cipher) != 0 {
		t.Fatal("legacy update credential not cleared")
	}
	if err = g.Refresh(ctx, true); err != nil {
		t.Fatal(err)
	}
	if g.Snapshot().Allowed || !g.Snapshot().ForceUpdate {
		t.Fatal("mandatory update allowed")
	}
	checker.err = errors.New("offline")
	restarted, err := safety.New(ctx, db.Pool(), checker, "fixture-upstream", protocol.Device{})
	if err != nil {
		t.Fatal(err)
	}
	_ = restarted.Refresh(ctx, true)
	if !restarted.Snapshot().ForceUpdate || restarted.Snapshot().Allowed {
		t.Fatal("restart/offline cleared forced update")
	}
	raw = requestTest(t, admin, server.URL, http.MethodGet, "/api/v1/portal/system/versions", nil, 200)
	if bytes.Contains(raw, []byte("fixture-private-token")) || !bytes.Contains(raw, []byte(`"force_update":true`)) {
		t.Fatal("unsafe admin snapshot")
	}
}
