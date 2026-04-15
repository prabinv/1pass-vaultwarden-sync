package config

import (
	"fmt"
	"os"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	OPServiceAccountToken    string
	VaultwardenURL           string
	VaultwardenClientID      string
	VaultwardenClientSecret  string
	VaultwardenMasterPassword string
}

// Load reads configuration from environment variables, returning an error if
// any required variable is missing.
func Load() (*Config, error) {
	c := &Config{
		OPServiceAccountToken:    os.Getenv("OP_SERVICE_ACCOUNT_TOKEN"),
		VaultwardenURL:           os.Getenv("VAULTWARDEN_URL"),
		VaultwardenClientID:      os.Getenv("VAULTWARDEN_CLIENT_ID"),
		VaultwardenClientSecret:  os.Getenv("VAULTWARDEN_CLIENT_SECRET"),
		VaultwardenMasterPassword: os.Getenv("VAULTWARDEN_MASTER_PASSWORD"),
	}

	required := []struct {
		name  string
		value string
	}{
		{"OP_SERVICE_ACCOUNT_TOKEN", c.OPServiceAccountToken},
		{"VAULTWARDEN_URL", c.VaultwardenURL},
		{"VAULTWARDEN_CLIENT_ID", c.VaultwardenClientID},
		{"VAULTWARDEN_CLIENT_SECRET", c.VaultwardenClientSecret},
		{"VAULTWARDEN_MASTER_PASSWORD", c.VaultwardenMasterPassword},
	}

	for _, r := range required {
		if r.value == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", r.name)
		}
	}

	return c, nil
}
