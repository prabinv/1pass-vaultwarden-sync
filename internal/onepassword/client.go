package onepassword

import (
	"context"
	"fmt"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

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
	sdk, err := op.NewClient(ctx,
		op.WithServiceAccountToken(token),
		op.WithIntegrationInfo("1pass-vaultwarden-sync", "v1.0.0"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating 1Password SDK client: %w", err)
	}
	return &Client{sdk: sdk}, nil
}

// ListItems implements sync.ItemSource by fetching all items across all vaults.
func (c *Client) ListItems(ctx context.Context) ([]syncp.Item, error) {
	vaults, err := c.sdk.Vaults().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing vaults: %w", err)
	}

	var items []syncp.Item
	for _, vault := range vaults {
		overviews, err := c.sdk.Items().List(ctx, vault.ID)
		if err != nil {
			return nil, fmt.Errorf("listing items in vault %s: %w", vault.ID, err)
		}

		for _, overview := range overviews {
			item, err := c.sdk.Items().Get(ctx, vault.ID, overview.ID)
			if err != nil {
				return nil, fmt.Errorf("getting item %s: %w", overview.ID, err)
			}

			mapped := sdkItemToSyncItem(item)
			items = append(items, mapped)
		}
	}

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
