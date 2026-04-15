package config_test

import (
	"testing"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c *config.Config)
	}{
		{
			name: "all required vars present",
			env: map[string]string{
				"OP_SERVICE_ACCOUNT_TOKEN":    "ops_abc123",
				"VAULTWARDEN_URL":             "https://vault.example.com",
				"VAULTWARDEN_CLIENT_ID":       "client-id",
				"VAULTWARDEN_CLIENT_SECRET":   "client-secret",
				"VAULTWARDEN_MASTER_PASSWORD": "hunter2",
			},
			wantErr: false,
			check: func(t *testing.T, c *config.Config) {
				t.Helper()
				if c.OPServiceAccountToken != "ops_abc123" {
					t.Errorf("OPServiceAccountToken = %q, want %q", c.OPServiceAccountToken, "ops_abc123")
				}
				if c.VaultwardenURL != "https://vault.example.com" {
					t.Errorf("VaultwardenURL = %q, want %q", c.VaultwardenURL, "https://vault.example.com")
				}
			},
		},
		{
			name: "missing OP_SERVICE_ACCOUNT_TOKEN",
			env: map[string]string{
				"VAULTWARDEN_URL":             "https://vault.example.com",
				"VAULTWARDEN_CLIENT_ID":       "client-id",
				"VAULTWARDEN_CLIENT_SECRET":   "client-secret",
				"VAULTWARDEN_MASTER_PASSWORD": "hunter2",
			},
			wantErr: true,
		},
		{
			name: "missing VAULTWARDEN_URL",
			env: map[string]string{
				"OP_SERVICE_ACCOUNT_TOKEN":    "ops_abc123",
				"VAULTWARDEN_CLIENT_ID":       "client-id",
				"VAULTWARDEN_CLIENT_SECRET":   "client-secret",
				"VAULTWARDEN_MASTER_PASSWORD": "hunter2",
			},
			wantErr: true,
		},
		{
			name: "missing VAULTWARDEN_CLIENT_ID",
			env: map[string]string{
				"OP_SERVICE_ACCOUNT_TOKEN":    "ops_abc123",
				"VAULTWARDEN_URL":             "https://vault.example.com",
				"VAULTWARDEN_CLIENT_SECRET":   "client-secret",
				"VAULTWARDEN_MASTER_PASSWORD": "hunter2",
			},
			wantErr: true,
		},
		{
			name: "missing VAULTWARDEN_CLIENT_SECRET",
			env: map[string]string{
				"OP_SERVICE_ACCOUNT_TOKEN":    "ops_abc123",
				"VAULTWARDEN_URL":             "https://vault.example.com",
				"VAULTWARDEN_CLIENT_ID":       "client-id",
				"VAULTWARDEN_MASTER_PASSWORD": "hunter2",
			},
			wantErr: true,
		},
		{
			name: "missing VAULTWARDEN_MASTER_PASSWORD",
			env: map[string]string{
				"OP_SERVICE_ACCOUNT_TOKEN":  "ops_abc123",
				"VAULTWARDEN_URL":           "https://vault.example.com",
				"VAULTWARDEN_CLIENT_ID":     "client-id",
				"VAULTWARDEN_CLIENT_SECRET": "client-secret",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env vars for this test.
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			c, err := config.Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}
