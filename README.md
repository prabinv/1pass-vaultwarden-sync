# 1pass-vaultwarden-sync

Syncs all items from 1Password → Vaultwarden (self-hosted Bitwarden). Items are only updated when the 1Password copy is newer. Features an interactive TUI with a live preview list.

## Prerequisites

- Go 1.21+
- A 1Password account (Business or Teams plan for Service Accounts)
- A running Vaultwarden instance

## Setup

### 1. 1Password Service Account Token

1. Sign in to your 1Password account at https://my.1password.com
2. Go to **Developer Tools → Service Accounts**
3. Click **Create a Service Account**
4. Give it a name (e.g. `vaultwarden-sync`)
5. Grant **read** access to the vaults you want to sync (or all vaults for a full sync)
6. Copy the generated token — it is shown only once
7. Set the env var:

```sh
export OP_SERVICE_ACCOUNT_TOKEN="ops_..."
```

> **Note:** Service Accounts require a 1Password Business or Teams plan. The token starts with `ops_`.

### 2. Vaultwarden API Credentials

In your Vaultwarden web vault:

1. Go to **Account Settings → Security → API Key**
2. Copy the **Client ID** and **Client Secret**

```sh
export VAULTWARDEN_URL="https://vault.example.com"
export VAULTWARDEN_CLIENT_ID="your-client-id"
export VAULTWARDEN_CLIENT_SECRET="your-client-secret"
export VAULTWARDEN_MASTER_PASSWORD="your-master-password"
```

> The master password is **not** used for authentication. It is used solely to derive the encryption key needed to encrypt vault items before uploading (Bitwarden uses end-to-end encryption).

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `OP_SERVICE_ACCOUNT_TOKEN` | yes | 1Password service account token (starts with `ops_`) |
| `VAULTWARDEN_URL` | yes | Base URL of your Vaultwarden instance |
| `VAULTWARDEN_CLIENT_ID` | yes | Vaultwarden API client ID |
| `VAULTWARDEN_CLIENT_SECRET` | yes | Vaultwarden API client secret |
| `VAULTWARDEN_MASTER_PASSWORD` | yes | Vaultwarden master password (for E2E encryption key derivation) |

## Usage

### One-shot sync (interactive TUI)

```sh
go run ./cmd/sync
```

The TUI will:
1. Fetch all items from 1Password
2. Show a scrollable list of items to be created or updated
3. Press `enter` to proceed, `q` to quit

### Watch mode (runs on interval)

```sh
go run ./cmd/sync --watch 5m
```

Syncs every 5 minutes. Skips the confirmation step automatically.

### Non-interactive (scripting / CI)

```sh
go run ./cmd/sync --no-confirm
```

Skips the TUI confirmation and writes plain log output.

## Build

```sh
go build -o 1pass-vaultwarden-sync ./cmd/sync
./1pass-vaultwarden-sync --watch 15m
```

## Development

```sh
# Run all unit tests with race detector
go test -race ./...

# Coverage report
go test -cover ./...

# Fuzz the sync engine timestamp logic (run for 30s)
go test -fuzz=FuzzTimestampCompare -fuzztime=30s ./internal/sync/

# Fuzz Vaultwarden JSON parsing
go test -fuzz=FuzzParseVaultwardenItem -fuzztime=30s ./internal/vaultwarden/

# Fuzz 1Password item deserialization
go test -fuzz=FuzzParseOPItem -fuzztime=30s ./internal/onepassword/
```
