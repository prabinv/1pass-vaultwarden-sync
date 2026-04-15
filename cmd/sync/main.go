package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/config"
	op "github.com/prabinv/1pass-vaultwarden-sync/internal/onepassword"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/tui"
	vw "github.com/prabinv/1pass-vaultwarden-sync/internal/vaultwarden"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		watchInterval string
		noConfirm     bool
	)

	cmd := &cobra.Command{
		Use:   "1pass-vaultwarden-sync",
		Short: "Sync all 1Password items → Vaultwarden",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Build 1Password source client.
			opClient, err := op.New(ctx, cfg.OPServiceAccountToken)
			if err != nil {
				return fmt.Errorf("initialising 1Password client: %w", err)
			}

			// Obtain Vaultwarden access token via client credentials.
			vwToken, err := getVaultwardenToken(cfg)
			if err != nil {
				return fmt.Errorf("authenticating with Vaultwarden: %w", err)
			}

			vwClient := vw.New(cfg.VaultwardenURL, vwToken)
			engine := syncp.NewEngine(opClient, vwClient)

			// Parse optional watch interval.
			var every time.Duration
			if watchInterval != "" {
				every, err = time.ParseDuration(watchInterval)
				if err != nil {
					return fmt.Errorf("invalid --watch value %q: %w", watchInterval, err)
				}
			}

			tuiCfg := tui.Config{
				AutoProceed: noConfirm || every > 0,
				WatchEvery:  every,
			}

			if every > 0 {
				return runWatch(ctx, engine, tuiCfg, every)
			}
			return runOnce(ctx, engine, tuiCfg)
		},
	}

	cmd.Flags().StringVar(&watchInterval, "watch", "", "run repeatedly on this interval (e.g. 5m, 1h)")
	cmd.Flags().BoolVar(&noConfirm, "no-confirm", false, "skip confirmation prompt and run non-interactively")

	return cmd
}

// runOnce runs a single sync with the TUI.
func runOnce(ctx context.Context, engine *syncp.Engine, cfg tui.Config) error {
	m := tui.New(ctx, engine, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}

// runWatch runs sync repeatedly on the given interval until the context is cancelled.
func runWatch(ctx context.Context, engine *syncp.Engine, cfg tui.Config, every time.Duration) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	if err := runOnce(ctx, engine, cfg); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := runOnce(ctx, engine, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "sync error: %v\n", err)
			}
		}
	}
}

// getVaultwardenToken authenticates using client credentials and returns a
// Bearer access token. The master password is used to derive the user's
// encryption key (handled inside the Vaultwarden client on write operations).
func getVaultwardenToken(cfg *config.Config) (string, error) {
	// TODO(Phase 4 integration): implement full Bitwarden identity token
	// exchange (POST /identity/token, grant_type=client_credentials) and
	// symmetric key derivation from master password for E2E encryption.
	// For now, return the client secret as a placeholder so the binary
	// compiles and wires up end-to-end.
	_ = cfg.VaultwardenMasterPassword // will be used in key derivation
	return cfg.VaultwardenClientSecret, nil
}
