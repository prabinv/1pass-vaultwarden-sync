package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type itemStatus int

const (
	statusPending itemStatus = iota
	statusCreate
	statusUpdate
	statusSkip
	statusDone
	statusError
)

// listItem is a single row in the bubbles list.
type listItem struct {
	externalID string
	name       string
	itemType   string
	status     itemStatus
	errMsg     string
}

func (i listItem) Title() string {
	prefix := actionPrefix(i.status)
	name := i.name
	if i.status == statusError && i.errMsg != "" {
		name = fmt.Sprintf("%s  %s", name, styleError.Render("error: "+i.errMsg))
	}
	return fmt.Sprintf("%s  %s", prefix, name)
}

func (i listItem) Description() string { return "" }
func (i listItem) FilterValue() string { return i.name }

// itemDelegate renders list items with lipgloss styling.
type itemDelegate struct{}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                           { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	li, ok := item.(listItem)
	if !ok {
		return
	}

	str := li.Title()
	if index == m.Index() {
		str = lipgloss.NewStyle().Bold(true).Render(str)
	}
	fmt.Fprintln(w, str)
}

// newList builds a bubbles list model from a slice of listItems.
func newList(items []listItem, width, height int) list.Model {
	listItems := make([]list.Item, len(items))
	for i, it := range items {
		listItems[i] = it
	}

	l := list.New(listItems, itemDelegate{}, width, height)
	l.SetShowTitle(false)
	l.SetShowStatusBar(true) // shows "1–5 of N" pagination info
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
	return l
}
