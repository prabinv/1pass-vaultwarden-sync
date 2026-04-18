package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/config"
	cryptopkg "github.com/prabinv/1pass-vaultwarden-sync/internal/crypto"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/logger"
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
		debug         bool
		logFile       string
		pageSize      int
	)

	cmd := &cobra.Command{
		Use:   "1pass-vaultwarden-sync",
		Short: "Sync all 1Password items → Vaultwarden",
		RunE: func(cmd *cobra.Command, args []string) error {
			// In TUI mode logs would interleave with the alternate-screen
			// rendering, so only enable them when running non-interactively or
			// when the user explicitly asks for debug output.
			var logWriter io.Writer = io.Discard
			if noConfirm || debug {
				logWriter = os.Stderr
			}
			if logFile != "" {
				f, err := os.Create(logFile)
				if err != nil {
					return fmt.Errorf("opening log file: %w", err)
				}
				defer f.Close()
				logWriter = f
			}
			logger.Setup(logWriter, debug)

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Initialise the 1Password client and Vaultwarden token concurrently —
			// they talk to independent services and neither depends on the other.
			var (
				opClient *op.Client
				vwToken  string
				userKey  *cryptopkg.UserKey
			)
			{
				g, gctx := errgroup.WithContext(ctx)
				g.Go(func() error {
					var err error
					opClient, err = op.New(gctx, cfg.OPServiceAccountToken)
					if err != nil {
						return fmt.Errorf("initialising 1Password client: %w", err)
					}
					return nil
				})
				g.Go(func() error {
					var err error
					vwToken, userKey, err = getVaultwardenToken(gctx, cfg)
					if err != nil {
						return fmt.Errorf("authenticating with Vaultwarden: %w", err)
					}
					return nil
				})
				if err := g.Wait(); err != nil {
					return err
				}
			}

			vwClient := vw.New(cfg.VaultwardenURL, vwToken, userKey)
			defer vwClient.Close()
			if err := vwClient.WarmCache(ctx); err != nil {
				return fmt.Errorf("warming Vaultwarden cache: %w", err)
			}
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
				PageSize:    pageSize,
			}

			if every > 0 {
				return runWatch(ctx, engine, tuiCfg, every)
			}
			return runOnce(ctx, engine, tuiCfg)
		},
	}

	cmd.Flags().StringVar(&watchInterval, "watch", "", "run repeatedly on this interval (e.g. 5m, 1h)")
	cmd.Flags().BoolVar(&noConfirm, "no-confirm", false, "skip confirmation prompt and run non-interactively")
	cmd.Flags().BoolVar(&debug, "debug", false, "enable debug-level structured logging to stderr")
	cmd.Flags().StringVar(&logFile, "log-file", "", "write logs to this file (useful alongside TUI mode)")
	cmd.Flags().IntVar(&pageSize, "page-size", 0, "items per page in the list (default 5)")

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

// getVaultwardenToken performs the Bitwarden identity token exchange and
// derives the user's symmetric encryption key from the master password.
// Returns the Bearer access token and the decrypted user key.
func getVaultwardenToken(ctx context.Context, cfg *config.Config) (string, *cryptopkg.UserKey, error) {
	return cryptopkg.Exchange(
		ctx,
		nil, // use http.DefaultClient
		cfg.VaultwardenURL,
		cfg.VaultwardenClientID,
		cfg.VaultwardenClientSecret,
		cfg.VaultwardenMasterPassword,
	)
}
