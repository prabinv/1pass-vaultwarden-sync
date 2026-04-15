# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Go tool to sync secrets between 1Password and Vaultwarden (self-hosted Bitwarden).

Module: `github.com/prabinv/1pass-vaultwarden-sync`

## Commands

```bash
# Build
go build ./...

# Run tests (always with race detector)
go test -race ./...

# Test single package
go test -race ./internal/sync/...

# Coverage
go test -cover ./...

# Format
gofmt -w .
goimports -w .

# Vet
go vet ./...

# Security scan
gosec ./...
```

## Architecture

This project is in early/empty state — only `go.mod` exists. Expected structure when built:

- **cmd/** — entry point(s)
- **internal/** — private packages (sync logic, 1Password client, Vaultwarden client)
- Credentials via env vars (`OP_TOKEN`, `VAULTWARDEN_URL`, `VAULTWARDEN_TOKEN`, etc.)

## Go Conventions

- Accept interfaces, return structs
- Wrap errors: `fmt.Errorf("context: %w", err)`
- Use `context.Context` with timeouts for all external calls
- Table-driven tests
