package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Credentials holds decrypted connection details. Never return in HTTP responses.
type Credentials struct {
	ID                        uuid.UUID
	UserID                    uuid.UUID
	OPServiceAccountToken     string
	VaultwardenURL            string
	VaultwardenClientID       string
	VaultwardenClientSecret   string
	VaultwardenMasterPassword string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type CredentialsStore struct {
	pool          *pgxpool.Pool
	encryptionKey string
}

func NewCredentialsStore(pool *pgxpool.Pool, encryptionKey string) *CredentialsStore {
	return &CredentialsStore{pool: pool, encryptionKey: encryptionKey}
}

// Upsert creates or replaces credentials for userID. Must run inside WithUserID (RLS).
func (s *CredentialsStore) Upsert(ctx context.Context, tx pgx.Tx, userID uuid.UUID, c *Credentials) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO credentials (
			user_id,
			op_service_account_token,
			vaultwarden_url,
			vaultwarden_client_id,
			vaultwarden_client_secret,
			vaultwarden_master_password,
			updated_at
		) VALUES (
			$1,
			pgp_sym_encrypt($2, $7),
			$3,
			pgp_sym_encrypt($4, $7),
			pgp_sym_encrypt($5, $7),
			pgp_sym_encrypt($6, $7),
			now()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			op_service_account_token    = pgp_sym_encrypt($2, $7),
			vaultwarden_url             = $3,
			vaultwarden_client_id       = pgp_sym_encrypt($4, $7),
			vaultwarden_client_secret   = pgp_sym_encrypt($5, $7),
			vaultwarden_master_password = pgp_sym_encrypt($6, $7),
			updated_at                  = now()`,
		userID,
		c.OPServiceAccountToken,
		c.VaultwardenURL,
		c.VaultwardenClientID,
		c.VaultwardenClientSecret,
		c.VaultwardenMasterPassword,
		s.encryptionKey,
	)
	if err != nil {
		return fmt.Errorf("upsert credentials: %w", err)
	}
	return nil
}

// Get returns decrypted credentials for userID, or ErrNotFound. Must run inside WithUserID (RLS).
func (s *CredentialsStore) Get(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*Credentials, error) {
	var c Credentials
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			user_id,
			pgp_sym_decrypt(op_service_account_token, $2),
			vaultwarden_url,
			pgp_sym_decrypt(vaultwarden_client_id, $2),
			pgp_sym_decrypt(vaultwarden_client_secret, $2),
			pgp_sym_decrypt(vaultwarden_master_password, $2),
			created_at,
			updated_at
		FROM credentials
		WHERE user_id = $1`,
		userID, s.encryptionKey,
	)
	if err := row.Scan(
		&c.ID, &c.UserID,
		&c.OPServiceAccountToken,
		&c.VaultwardenURL,
		&c.VaultwardenClientID,
		&c.VaultwardenClientSecret,
		&c.VaultwardenMasterPassword,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get credentials: %w", err)
	}
	return &c, nil
}
