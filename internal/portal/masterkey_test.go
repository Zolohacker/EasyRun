package portal

import (
	"context"
	"testing"
)

func TestEncryptedDeploymentIsDetectedBeforeMasterKeyGeneration(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	exists, err := db.HasEncryptedSecrets(ctx)
	if err != nil || exists {
		t.Fatalf("fresh database: %v %v", exists, err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE portal_settings SET secrets='{"seed":"fixture-cipher"}'::jsonb WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	exists, err = db.HasEncryptedSecrets(ctx)
	if err != nil || !exists {
		t.Fatal("encrypted wallet not detected")
	}
	if _, err := s.db.Exec(ctx, `UPDATE portal_settings SET secrets='{}'::jsonb WHERE id=1;INSERT INTO system_settings(singleton_id,map_api_key_cipher,map_qps,map_api_keys,worker_processes,worker_concurrency)VALUES(1,'fixture-cipher'::bytea,3,'[]'::jsonb,2,1)`); err != nil {
		t.Fatal(err)
	}
	exists, err = db.HasEncryptedSecrets(ctx)
	if err != nil || !exists {
		t.Fatal("encrypted map settings not detected")
	}
}
