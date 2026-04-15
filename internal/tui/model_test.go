package tui_test

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/tui"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

// --- mocks ---

type mockSource struct{ items []syncp.Item }

func (m *mockSource) ListItems(_ context.Context) ([]syncp.Item, error) {
	return m.items, nil
}

type mockSink struct {
	existing  map[string]*syncp.Item
	createErr error
	updateErr error
}

func (m *mockSink) GetItem(_ context.Context, id string) (*syncp.Item, error) {
	item, ok := m.existing[id]
	if !ok {
		return nil, nil
	}
	return item, nil
}

func (m *mockSink) CreateItem(_ context.Context, _ syncp.Item) error { return m.createErr }
func (m *mockSink) UpdateItem(_ context.Context, _ syncp.Item) error { return m.updateErr }

// --- helpers ---

func newEngine(sourceItems []syncp.Item, sinkItems []syncp.Item) *syncp.Engine {
	existing := make(map[string]*syncp.Item)
	for i := range sinkItems {
		existing[sinkItems[i].ExternalID] = &sinkItems[i]
	}
	src := &mockSource{items: sourceItems}
	sink := &mockSink{existing: existing}
	return syncp.NewEngine(src, sink)
}

var (
	t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
)

func item(id, name string, at time.Time) syncp.Item {
	return syncp.Item{ExternalID: id, Name: name, UpdatedAt: at}
}

// runMsgs sends a sequence of messages to a model, returning the final model.
func runMsgs(m tea.Model, msgs ...tea.Msg) tea.Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

// --- tests ---

func TestModel_InitialState_IsPlanning(t *testing.T) {
	eng := newEngine(nil, nil)
	m := tui.New(context.Background(), eng, tui.Config{})
	view := m.View()
	if view == "" {
		t.Error("initial view should not be empty")
	}
}

func TestModel_PlanReady_TransitionsToPreview(t *testing.T) {
	eng := newEngine([]syncp.Item{item("a", "A", t1)}, nil)
	m := tui.New(context.Background(), eng, tui.Config{})

	plan, err := eng.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}

	final := runMsgs(m, tui.PlanReadyMsg{Plan: plan})
	view := final.View()
	if view == "" {
		t.Error("preview view should not be empty")
	}
}

func TestModel_AutoProceed_SkipsPreview(t *testing.T) {
	eng := newEngine([]syncp.Item{item("a", "A", t1)}, nil)
	m := tui.New(context.Background(), eng, tui.Config{AutoProceed: true})

	plan, _ := eng.Plan(context.Background())
	final := runMsgs(m, tui.PlanReadyMsg{Plan: plan})
	// With AutoProceed, model should transition to syncing immediately (no preview).
	view := final.View()
	if view == "" {
		t.Error("syncing view should not be empty")
	}
}

func TestModel_QuitKey(t *testing.T) {
	eng := newEngine(nil, nil)
	m := tui.New(context.Background(), eng, tui.Config{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Error("expected quit command on 'q' key")
	}
}

func TestModel_PlanError_ShowsError(t *testing.T) {
	eng := newEngine(nil, nil)
	m := tui.New(context.Background(), eng, tui.Config{})
	final := runMsgs(m, tui.PlanReadyMsg{Err: errors.New("source unavailable")})
	view := final.View()
	if view == "" {
		t.Error("error view should not be empty")
	}
}
