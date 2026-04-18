package sync_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// --- mocks ---

type mockSource struct {
	items []syncp.Item
	err   error
}

func (m *mockSource) ListItems(_ context.Context) ([]syncp.Item, error) {
	return m.items, m.err
}

type mockSink struct {
	items map[string]*syncp.Item
	getErr    error
	createErr error
	updateErr error
	created   []syncp.Item
	updated   []syncp.Item
}

func newMockSink(items ...syncp.Item) *mockSink {
	s := &mockSink{items: make(map[string]*syncp.Item)}
	for i := range items {
		s.items[items[i].Name] = &items[i]
	}
	return s
}

func (m *mockSink) GetItem(_ context.Context, name string) (*syncp.Item, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	item, ok := m.items[name]
	if !ok {
		return nil, nil
	}
	return item, nil
}

func (m *mockSink) CreateItem(_ context.Context, item syncp.Item) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.created = append(m.created, item)
	return nil
}

func (m *mockSink) UpdateItem(_ context.Context, item syncp.Item) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updated = append(m.updated, item)
	return nil
}

// --- helpers ---

var (
	t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
	t2 = t0.Add(2 * time.Hour)
)

func item(id, name string, updatedAt time.Time) syncp.Item {
	return syncp.Item{ExternalID: id, Name: name, UpdatedAt: updatedAt}
}

// --- plan tests ---

func TestEngine_Plan(t *testing.T) {
	tests := []struct {
		name         string
		sourceItems  []syncp.Item
		sinkItems    []syncp.Item
		wantCreates  int
		wantUpdates  int
		wantSkips    int
	}{
		{
			name:        "all items new",
			sourceItems: []syncp.Item{item("a", "A", t1), item("b", "B", t1)},
			sinkItems:   nil,
			wantCreates: 2,
		},
		{
			name:        "all items exist and are newer in sink",
			sourceItems: []syncp.Item{item("a", "A", t1)},
			sinkItems:   []syncp.Item{item("a", "A", t2)},
			wantSkips:   1,
		},
		{
			name:        "item exists and is newer in source",
			sourceItems: []syncp.Item{item("a", "A", t2)},
			sinkItems:   []syncp.Item{item("a", "A", t1)},
			wantUpdates: 1,
		},
		{
			name:        "item exists with same timestamp",
			sourceItems: []syncp.Item{item("a", "A", t1)},
			sinkItems:   []syncp.Item{item("a", "A", t1)},
			wantSkips:   1,
		},
		{
			name:        "mixed creates updates skips",
			sourceItems: []syncp.Item{
				item("create-me", "C", t1),
				item("update-me", "U", t2),
				item("skip-me", "S", t1),
			},
			sinkItems: []syncp.Item{
				item("update-me", "U", t1),
				item("skip-me", "S", t2),
			},
			wantCreates: 1,
			wantUpdates: 1,
			wantSkips:   1,
		},
		{
			name:        "empty source",
			sourceItems: nil,
			sinkItems:   []syncp.Item{item("a", "A", t1)},
			wantSkips:   0,
			wantCreates: 0,
			wantUpdates: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &mockSource{items: tt.sourceItems}
			sink := newMockSink(tt.sinkItems...)
			eng := syncp.NewEngine(src, sink)

			plan, err := eng.Plan(context.Background())
			if err != nil {
				t.Fatalf("Plan() unexpected error: %v", err)
			}

			creates, updates, skips := plan.Counts()
			if creates != tt.wantCreates {
				t.Errorf("creates = %d, want %d", creates, tt.wantCreates)
			}
			if updates != tt.wantUpdates {
				t.Errorf("updates = %d, want %d", updates, tt.wantUpdates)
			}
			if skips != tt.wantSkips {
				t.Errorf("skips = %d, want %d", skips, tt.wantSkips)
			}
		})
	}
}

func TestEngine_Plan_SourceError(t *testing.T) {
	src := &mockSource{err: errors.New("source unavailable")}
	sink := newMockSink()
	eng := syncp.NewEngine(src, sink)

	_, err := eng.Plan(context.Background())
	if err == nil {
		t.Fatal("expected error from source, got nil")
	}
}

// --- apply tests ---

func TestEngine_Apply(t *testing.T) {
	src := &mockSource{items: []syncp.Item{
		item("new", "New", t1),
		item("old", "Old", t2),
	}}
	sink := newMockSink(item("old", "Old", t1))
	eng := syncp.NewEngine(src, sink)

	plan, err := eng.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}

	var events []syncp.ProgressEvent
	ch := make(chan syncp.ProgressEvent, 10)
	go func() {
		eng.Apply(context.Background(), plan, ch)
		close(ch)
	}()
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if len(sink.created) != 1 {
		t.Errorf("created %d items, want 1", len(sink.created))
	}
	if len(sink.updated) != 1 {
		t.Errorf("updated %d items, want 1", len(sink.updated))
	}
}

func TestEngine_Apply_PropagatesErrors(t *testing.T) {
	src := &mockSource{items: []syncp.Item{item("a", "A", t1)}}
	sink := newMockSink()
	sink.createErr = errors.New("write failed")
	eng := syncp.NewEngine(src, sink)

	plan, err := eng.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}

	ch := make(chan syncp.ProgressEvent, 10)
	go func() {
		eng.Apply(context.Background(), plan, ch)
		close(ch)
	}()

	var errs int
	for e := range ch {
		if e.Err != nil {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("got %d error events, want 1", errs)
	}
}

// --- concurrency tests ---

// blockingSink is a sink whose CreateItem blocks until the context is cancelled.
type blockingSink struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingSink() *blockingSink {
	return &blockingSink{started: make(chan struct{})}
}

func (b *blockingSink) GetItem(_ context.Context, _ string) (*syncp.Item, error) { return nil, nil }
func (b *blockingSink) UpdateItem(_ context.Context, _ syncp.Item) error          { return nil }
func (b *blockingSink) CreateItem(ctx context.Context, _ syncp.Item) error {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return ctx.Err()
}

// TestApply_ContextCancellation verifies that Apply returns promptly when the
// context is cancelled mid-sync, with no goroutine leak.
func TestApply_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := newBlockingSink()
	src := &mockSource{items: []syncp.Item{item("a", "A", t1)}}
	eng := syncp.NewEngine(src, sink)

	plan, err := eng.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}

	ch := make(chan syncp.ProgressEvent, len(plan.Items)+1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		eng.Apply(ctx, plan, ch)
		close(ch)
	}()

	<-sink.started // wait until CreateItem is blocking
	cancel()

	select {
	case <-done:
		// Apply returned promptly after context cancellation — no goroutine leak.
	case <-time.After(2 * time.Second):
		t.Fatal("Apply did not return after context cancellation")
	}
}

// TestApply_Concurrent runs Apply from multiple goroutines in parallel.
// go test -race will surface any data races within Engine or its dependencies.
func TestApply_Concurrent(t *testing.T) {
	const workers = 4
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			src := &mockSource{items: []syncp.Item{item("a", "A", t1)}}
			eng := syncp.NewEngine(src, newMockSink())
			plan, err := eng.Plan(context.Background())
			if err != nil {
				t.Errorf("Plan(): %v", err)
				return
			}
			ch := make(chan syncp.ProgressEvent, len(plan.Items)+1)
			eng.Apply(context.Background(), plan, ch)
			close(ch)
			for range ch {
			}
		}()
	}
	wg.Wait()
}

// --- fuzz ---

func FuzzTimestampCompare(f *testing.F) {
	f.Add(t0.UnixNano(), t1.UnixNano())
	f.Add(t1.UnixNano(), t0.UnixNano())
	f.Add(t0.UnixNano(), t0.UnixNano())
	f.Add(int64(0), int64(0))
	f.Add(int64(-1), int64(1))

	f.Fuzz(func(t *testing.T, srcNano, sinkNano int64) {
		src := time.Unix(0, srcNano).UTC()
		sink := time.Unix(0, sinkNano).UTC()

		// Property: result must be exactly one of create/update/skip.
		sourceItem := syncp.Item{ExternalID: "x", Name: "x", UpdatedAt: src}
		sinkItem := syncp.Item{ExternalID: "x", Name: "x", UpdatedAt: sink}

		mockSrc := &mockSource{items: []syncp.Item{sourceItem}}
		mockSnk := newMockSink(sinkItem)
		eng := syncp.NewEngine(mockSrc, mockSnk)

		plan, err := eng.Plan(context.Background())
		if err != nil {
			t.Fatalf("Plan() error: %v", err)
		}
		if len(plan.Items) != 1 {
			t.Fatalf("expected 1 plan item, got %d", len(plan.Items))
		}
		a := plan.Items[0].Action
		if a != syncp.ActionCreate && a != syncp.ActionUpdate && a != syncp.ActionSkip {
			t.Errorf("unexpected action: %d", a)
		}
	})
}
