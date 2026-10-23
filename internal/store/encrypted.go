package store

import "context"

// HasEncryptedSecrets prevents inventing a new master key for an existing
// deployment whose original key file was lost. The portal table is optional
// before its first migration; application tables are already migrated here.
func (s *PostgresStore) HasEncryptedSecrets(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runner_accounts WHERE octet_length(password_cipher)>0 OR octet_length(session_cipher)>0 OR octet_length(face_base_cipher)>0 OR octet_length(face_custom_cipher)>0) OR EXISTS(SELECT 1 FROM system_settings WHERE octet_length(map_api_key_cipher)>0 OR map_api_keys<>'[]'::jsonb)`).Scan(&exists); err != nil || exists {
		return exists, err
	}
	var portal bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('portal_settings') IS NOT NULL`).Scan(&portal); err != nil {
		return false, err
	}
	if portal {
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_settings WHERE secrets<>'{}'::jsonb)`).Scan(&exists); err != nil {
			return false, err
		}
	}
	return exists, nil
}
