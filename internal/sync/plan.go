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
	// GetItem looks up an item by name. Returns nil if not found.
	// The returned Item's ExternalID is the sink's native ID (e.g. Vaultwarden UUID).
	GetItem(ctx context.Context, name string) (*Item, error)
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

// String returns a human-readable name for the action, used in logs and TUI output.
func (a Action) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	default:
		return "skip"
	}
}

// PlanItem pairs a source item with the action to be taken.
// SinkExternalID is the sink's native ID for the matching item (non-empty only
// for ActionUpdate). It is used to address the correct record on writes.
type PlanItem struct {
	Item           Item
	SinkExternalID string
	Action         Action
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
