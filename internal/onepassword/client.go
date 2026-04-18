package onepassword

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

// maxConcurrentFetches caps the number of simultaneous Items.Get calls so we
// don't overwhelm the 1Password API with hundreds of parallel requests.
const maxConcurrentFetches = 10

// RawItem mirrors an item as it would appear when unmarshalled from JSON.
// Used by MapItem and fuzz tests.
type RawItem struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Category  string     `json:"category"`
	UpdatedAt string     `json:"updated_at"`
	Fields    []RawField `json:"fields"`
}

// RawField mirrors a single field from a RawItem.
type RawField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// MapItem converts a RawItem into the canonical sync.Item.
func MapItem(raw RawItem) (syncp.Item, error) {
	updatedAt, err := time.Parse(time.RFC3339, raw.UpdatedAt)
	if err != nil {
		return syncp.Item{}, fmt.Errorf("parsing updated_at %q: %w", raw.UpdatedAt, err)
	}

	fields := make(map[string]string, len(raw.Fields))
	for _, f := range raw.Fields {
		fields[f.ID] = f.Value
	}

	return syncp.Item{
		ExternalID: raw.ID,
		Name:       raw.Title,
		Type:       raw.Category,
		Fields:     fields,
		UpdatedAt:  updatedAt.UTC(),
	}, nil
}

// Client wraps the 1Password SDK and implements sync.ItemSource.
type Client struct {
	sdk *op.Client
}

// New creates a new Client authenticated with the given service account token.
func New(ctx context.Context, token string) (*Client, error) {
	slog.DebugContext(ctx, "initialising 1Password SDK client")
	sdk, err := op.NewClient(ctx,
		op.WithServiceAccountToken(token),
		op.WithIntegrationInfo("1pass-vaultwarden-sync", "v1.0.0"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating 1Password SDK client: %w", err)
	}
	return &Client{sdk: sdk}, nil
}

// vaultOverview pairs an item overview ID with its parent vault metadata.
type vaultOverview struct {
	vaultID    string
	vaultTitle string
	overviewID string
}

// ListItems implements sync.ItemSource by fetching all items across all vaults.
// Phase 1: vault overview lists are fetched concurrently (one goroutine per vault).
// Phase 2: full item details are fetched concurrently, bounded by maxConcurrentFetches.
func (c *Client) ListItems(ctx context.Context) ([]syncp.Item, error) {
	slog.InfoContext(ctx, "listing 1Password vaults")

	vaults, err := c.sdk.Vaults().List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list vaults", slog.Any("error", err))
		return nil, fmt.Errorf("listing vaults: %w", err)
	}
	slog.DebugContext(ctx, "vaults found", slog.Int("count", len(vaults)))

	// Phase 1: collect all (vault, overview) pairs concurrently.
	var (
		mu        sync.Mutex
		overviews []vaultOverview
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, vault := range vaults {
		vault := vault
		g.Go(func() error {
			slog.DebugContext(gctx, "listing vault items",
				slog.String("vault", vault.Title),
				slog.String("vault_id", vault.ID))
			ovs, err := c.sdk.Items().List(gctx, vault.ID)
			if err != nil {
				slog.ErrorContext(gctx, "failed to list items in vault",
					slog.String("vault", vault.Title),
					slog.Any("error", err))
				return fmt.Errorf("listing items in vault %s: %w", vault.ID, err)
			}
			slog.DebugContext(gctx, "vault overviews fetched",
				slog.String("vault", vault.Title),
				slog.Int("count", len(ovs)))
			mu.Lock()
			for _, ov := range ovs {
				overviews = append(overviews, vaultOverview{
					vaultID:    vault.ID,
					vaultTitle: vault.Title,
					overviewID: ov.ID,
				})
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Phase 2: fetch full item details concurrently, bounded by semaphore.
	sem := semaphore.NewWeighted(maxConcurrentFetches)
	items := make([]syncp.Item, len(overviews))
	g, gctx = errgroup.WithContext(ctx)
	for i, ov := range overviews {
		i, ov := i, ov
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			item, err := c.sdk.Items().Get(gctx, ov.vaultID, ov.overviewID)
			if err != nil {
				slog.ErrorContext(gctx, "failed to get item",
					slog.String("id", ov.overviewID),
					slog.String("vault", ov.vaultTitle),
					slog.Any("error", err))
				return fmt.Errorf("getting item %s: %w", ov.overviewID, err)
			}
			items[i] = sdkItemToSyncItem(item)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "1Password items fetched", slog.Int("total", len(items)))
	return items, nil
}

// sdkItemToSyncItem converts a native SDK Item to a sync.Item.
func sdkItemToSyncItem(item op.Item) syncp.Item {
	fields := make(map[string]string, len(item.Fields))
	for _, f := range item.Fields {
		fields[f.ID] = f.Value
	}

	return syncp.Item{
		ExternalID: item.ID,
		Name:       item.Title,
		Type:       string(item.Category),
		Fields:     fields,
		UpdatedAt:  item.UpdatedAt.UTC(),
	}
}
