package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

// progressMsg carries a single ProgressEvent from the sync engine to the TUI.
type progressMsg syncp.ProgressEvent

// waitForProgress returns a tea.Cmd that reads one event from the channel.
// Returns nil when the channel is closed.
func waitForProgress(ch <-chan syncp.ProgressEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return syncDoneMsg{}
		}
		return progressMsg(event)
	}
}

// syncDoneMsg signals that the sync engine has finished applying all changes.
type syncDoneMsg struct{}

// PlanReadyMsg carries a completed SyncPlan from the planning goroutine.
// Exported so tests can inject it directly.
type PlanReadyMsg struct {
	Plan syncp.SyncPlan
	Err  error
}
