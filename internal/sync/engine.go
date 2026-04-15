package sync

import (
	"context"
	"fmt"
)

// Engine orchestrates the sync between a source and a sink.
type Engine struct {
	source ItemSource
	sink   ItemSink
}

// NewEngine constructs a new Engine.
func NewEngine(source ItemSource, sink ItemSink) *Engine {
	return &Engine{source: source, sink: sink}
}

// Plan fetches all items from the source, diffs them against the sink, and
// returns a SyncPlan describing what needs to be created, updated, or skipped.
// No writes are performed.
func (e *Engine) Plan(ctx context.Context) (SyncPlan, error) {
	items, err := e.source.ListItems(ctx)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("listing source items: %w", err)
	}

	plan := SyncPlan{Items: make([]PlanItem, 0, len(items))}

	for _, src := range items {
		existing, err := e.sink.GetItem(ctx, src.ExternalID)
		if err != nil {
			return SyncPlan{}, fmt.Errorf("getting sink item %s: %w", src.ExternalID, err)
		}

		var action Action
		switch {
		case existing == nil:
			action = ActionCreate
		case src.UpdatedAt.After(existing.UpdatedAt):
			action = ActionUpdate
		default:
			action = ActionSkip
		}

		plan.Items = append(plan.Items, PlanItem{Item: src, Action: action})
	}

	return plan, nil
}

// Apply executes a SyncPlan, sending a ProgressEvent per item to the provided
// channel. Callers must drain the channel; Apply does not close it.
func (e *Engine) Apply(ctx context.Context, plan SyncPlan, events chan<- ProgressEvent) {
	for _, pi := range plan.Items {
		if pi.Action == ActionSkip {
			events <- ProgressEvent{Item: pi.Item, Action: ActionSkip}
			continue
		}

		var err error
		switch pi.Action {
		case ActionCreate:
			err = e.sink.CreateItem(ctx, pi.Item)
		case ActionUpdate:
			err = e.sink.UpdateItem(ctx, pi.Item)
		}

		events <- ProgressEvent{Item: pi.Item, Action: pi.Action, Err: err}
	}
}
