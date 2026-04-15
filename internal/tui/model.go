package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

type state int

const (
	statePlanning state = iota
	statePreview
	stateSyncing
	stateDone
)

// Config holds the options that control TUI behaviour.
type Config struct {
	AutoProceed bool          // skip confirmation (watch mode / --no-confirm)
	WatchEvery  time.Duration // non-zero → show "Next sync in …" countdown
}

// Model is the root Bubble Tea model.
type Model struct {
	cfg     Config
	engine  *syncp.Engine
	ctx     context.Context
	cancel  context.CancelFunc

	state   state
	spinner spinner.Model
	list    list.Model
	items   []listItem // parallel to list items for status updates

	plan        syncp.SyncPlan
	progressCh  chan syncp.ProgressEvent
	result      syncp.SyncResult
	planErr     error
	nextSyncAt  time.Time
	windowWidth int
	windowHeight int
}

// New constructs the root TUI Model.
func New(ctx context.Context, engine *syncp.Engine, cfg Config) Model {
	ctx, cancel := context.WithCancel(ctx)
	s := spinner.New()
	s.Spinner = spinner.Dot
	return Model{
		cfg:    cfg,
		engine: engine,
		ctx:    ctx,
		cancel: cancel,
		state:  statePlanning,
		spinner: s,
	}
}

// Init starts the spinner and kicks off the planning goroutine.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.doPlan(),
	)
}

func (m Model) doPlan() tea.Cmd {
	return func() tea.Msg {
		plan, err := m.engine.Plan(m.ctx)
		return PlanReadyMsg{Plan: plan, Err: err}
	}
}

// Update handles all incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.windowHeight = msg.Height
		if m.state == statePreview || m.state == stateSyncing {
			m.list.SetSize(msg.Width, m.listHeight())
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case PlanReadyMsg:
		if msg.Err != nil {
			m.planErr = msg.Err
			m.state = stateDone
			return m, nil
		}
		m.plan = msg.Plan
		m.items = planToListItems(msg.Plan)
		m.list = newList(m.items, m.windowWidth, m.listHeight())

		if m.cfg.AutoProceed {
			return m, m.doApply()
		}
		m.state = statePreview
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "enter":
			if m.state == statePreview {
				return m, m.doApply()
			}
		}
		if m.state == statePreview {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}

	case progressMsg:
		m.applyProgress(syncp.ProgressEvent(msg))
		return m, waitForProgress(m.progressCh)

	case syncDoneMsg:
		m.state = stateDone
		if m.cfg.WatchEvery > 0 {
			m.nextSyncAt = time.Now().Add(m.cfg.WatchEvery)
		}
		return m, nil
	}

	return m, nil
}

func (m *Model) doApply() tea.Cmd {
	m.state = stateSyncing
	m.progressCh = make(chan syncp.ProgressEvent, len(m.plan.Items)+1)
	go func() {
		m.engine.Apply(m.ctx, m.plan, m.progressCh)
		close(m.progressCh)
	}()
	return waitForProgress(m.progressCh)
}

func (m *Model) applyProgress(ev syncp.ProgressEvent) {
	for i, it := range m.items {
		if it.externalID != ev.Item.ExternalID {
			continue
		}
		switch {
		case ev.Err != nil:
			m.items[i].status = statusError
			m.items[i].errMsg = ev.Err.Error()
			m.result.Errors++
		case ev.Action == syncp.ActionCreate:
			m.items[i].status = statusDone
			m.result.Created++
		case ev.Action == syncp.ActionUpdate:
			m.items[i].status = statusDone
			m.result.Updated++
		default:
			m.items[i].status = statusSkip
			m.result.Skipped++
		}

		// Rebuild the list item in-place.
		updated := make([]list.Item, len(m.items))
		for j, li := range m.items {
			updated[j] = li
		}
		m.list.SetItems(updated)
		break
	}
}

func (m Model) listHeight() int {
	if m.windowHeight > 8 {
		return m.windowHeight - 6
	}
	return 10
}

// View renders the current UI state.
func (m Model) View() string {
	var b strings.Builder

	switch m.state {
	case statePlanning:
		fmt.Fprintf(&b, "\n  %s Fetching items from 1Password…\n", m.spinner.View())

	case statePreview:
		creates, updates, _ := m.plan.Counts()
		b.WriteString(styleTitle.Render(
			fmt.Sprintf("  Pending Changes  %s  %s",
				styleCreate.Render(fmt.Sprintf("+%d", creates)),
				styleUpdate.Render(fmt.Sprintf("↑%d", updates)),
			),
		))
		b.WriteString("\n")
		b.WriteString(m.list.View())
		b.WriteString("\n")
		b.WriteString(styleHelp.Render("  j/k scroll  •  enter proceed  •  q quit"))

	case stateSyncing:
		b.WriteString(styleTitle.Render("  Syncing…"))
		b.WriteString("\n")
		b.WriteString(m.list.View())

	case stateDone:
		if m.planErr != nil {
			b.WriteString(styleError.Render(fmt.Sprintf("\n  Error: %v\n", m.planErr)))
			break
		}
		summary := fmt.Sprintf(
			"  %s created  %s updated  %s skipped  %s errors",
			styleCreate.Render(fmt.Sprintf("%d", m.result.Created)),
			styleUpdate.Render(fmt.Sprintf("%d", m.result.Updated)),
			styleSkip.Render(fmt.Sprintf("%d", m.result.Skipped)),
			styleError.Render(fmt.Sprintf("%d", m.result.Errors)),
		)
		b.WriteString(styleSummaryBox.Render(summary))
		if m.cfg.WatchEvery > 0 && !m.nextSyncAt.IsZero() {
			remaining := time.Until(m.nextSyncAt).Round(time.Second)
			b.WriteString("\n")
			b.WriteString(styleCountdown.Render(fmt.Sprintf("  Next sync in %s", remaining)))
		}
	}

	return b.String()
}

// planToListItems converts a SyncPlan into the TUI's listItem slice.
func planToListItems(plan syncp.SyncPlan) []listItem {
	items := make([]listItem, 0, len(plan.Items))
	for _, pi := range plan.Items {
		var st itemStatus
		switch pi.Action {
		case syncp.ActionCreate:
			st = statusCreate
		case syncp.ActionUpdate:
			st = statusUpdate
		default:
			st = statusSkip
		}
		items = append(items, listItem{
			externalID: pi.Item.ExternalID,
			name:       pi.Item.Name,
			itemType:   pi.Item.Type,
			status:     st,
		})
	}
	return items
}
