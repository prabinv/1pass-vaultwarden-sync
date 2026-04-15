package sync

import (
	"context"
	"time"
)

// Item is the canonical representation of a vault item used throughout the sync pipeline.
type Item struct {
	ExternalID string
	Name       string
	Type       string
	Fields     map[string]string
	UpdatedAt  time.Time
}

// ItemSource reads items from a source vault (e.g. 1Password).
type ItemSource interface {
	ListItems(ctx context.Context) ([]Item, error)
}

// ItemSink reads and writes items to a destination vault (e.g. Vaultwarden).
type ItemSink interface {
	GetItem(ctx context.Context, externalID string) (*Item, error)
	CreateItem(ctx context.Context, item Item) error
	UpdateItem(ctx context.Context, item Item) error
}

// Action describes what the sync engine will do with an item.
type Action int

const (
	ActionCreate Action = iota
	ActionUpdate
	ActionSkip
)

// PlanItem pairs a source item with the action to be taken.
type PlanItem struct {
	Item   Item
	Action Action
}

// SyncPlan is the result of diffing source against sink before any writes occur.
type SyncPlan struct {
	Items []PlanItem
}

// Counts returns the number of creates, updates, and skips in the plan.
func (p SyncPlan) Counts() (creates, updates, skips int) {
	for _, pi := range p.Items {
		switch pi.Action {
		case ActionCreate:
			creates++
		case ActionUpdate:
			updates++
		case ActionSkip:
			skips++
		}
	}
	return
}

// ProgressEvent reports the outcome of applying a single PlanItem.
type ProgressEvent struct {
	Item  Item
	Action Action
	Err   error
}

// SyncResult summarises a completed sync run.
type SyncResult struct {
	Created int
	Updated int
	Skipped int
	Errors  int
}
