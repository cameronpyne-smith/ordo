package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

func (m Model) finish(t api.Task, from mode) (tea.Model, tea.Cmd) {
	m.tookFrom, m.failure = from, ""
	return m, m.logged(func(c *client.Client) (string, int64, error) { return complete(c, t.ID) })
}

// complete says what finishing the task changed, and names it as the task
// to see off the screen.
func complete(c *client.Client, id int64) (string, int64, error) {
	t, err := c.Done(id, 0)
	if err != nil {
		return "", 0, err
	}
	note := "done"
	if t.Status == "open" {
		note += " — next one " + api.ShowDate(t.Due)
	}
	if waits := waitingOn(*t); waits != "" && t.Status == "done" {
		note += " — it was waiting on " + waits
	}
	return said(note, t.Outcome), t.ID, nil
}

// said adds what an outcome changed to the footer note.
func said(note string, o *api.Outcome) string {
	for _, s := range o.Said() {
		note += " — " + s
	}
	return note
}
