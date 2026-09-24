package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

// askWorked logs a session on a task that leaves some of it to do. It asks
// the same "how long" as done, prefilled with the block when it came from
// the day, then what is left, because time spent is not progress: two hours
// in, the job can turn out to have four still to go.
func (m Model) askWorked(t api.Task, took int, from mode) (tea.Model, tea.Cmd) {
	if t.Recur != nil {
		m.failure = "a repeating task is done in one go: d completes it"
		return m, nil
	}
	m.mode, m.took, m.tookFrom, m.working, m.failure = modeTook, t, from, true, ""
	m.input.Placeholder = "unknown"
	m.input.SetValue("")
	if took > 0 {
		m.input.SetValue(strconv.Itoa(took))
	}
	m.input.CursorEnd()
	m.input.Focus()
	return m, textinput.Blink
}

// askLeft is the second line, prefilled with what was left less what was
// just spent, so the usual case is two presses of enter.
func (m Model) askLeft(minutes int) (tea.Model, tea.Cmd) {
	m.mode, m.worked, m.failure = modeLeft, minutes, ""
	m.input.Placeholder = ""
	m.input.SetValue("")
	if before := leftOf(m.took); before > 0 {
		m.input.SetValue(strconv.Itoa(max(before-minutes, 0)))
	}
	m.input.CursorEnd()
	return m, nil
}

// leftOf is what a task had to go before this session: what the last one
// left, or its estimate when none has been logged.
func leftOf(t api.Task) int {
	if t.RemainingMinutes > 0 {
		return t.RemainingMinutes
	}
	return t.EstimateMinutes
}

// keyLeft sends the session. An empty line leaves the arithmetic to the
// daemon, which refuses it for a task with no estimate to count down from.
func (m Model) keyLeft(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode, m.working = m.tookFrom, false
		m.input.Blur()
		return m, nil
	case "enter":
		left, err := readLeft(m.input.Value())
		if err != nil {
			m.failure = err.Error()
			return m, nil
		}
		m.mode, m.working, m.failure = m.tookFrom, false, ""
		m.input.Blur()
		id, minutes := m.took.ID, m.worked
		return m, m.logged(func(c *client.Client) (string, int64, error) { return work(c, id, minutes, left) })
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func readLeft(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("%q is not a number of minutes; 0 means it is finished", value)
	}
	return &n, nil
}

func work(c *client.Client, id int64, minutes int, left *int) (string, int64, error) {
	t, err := c.Work(id, minutes, left)
	if err != nil {
		return "", 0, err
	}
	spent := "logged"
	if minutes > 0 {
		spent = fmt.Sprintf("%d min logged", minutes)
	}
	if t.Status == "done" {
		note := "done"
		if minutes > 0 {
			note = fmt.Sprintf("done in %d min", minutes)
		}
		return said(note, t.Outcome), t.ID, nil
	}
	return fmt.Sprintf("%s — %d left", spent, t.RemainingMinutes), 0, nil
}

// logged sends a completion or a session and refreshes whichever view it was
// asked from, since either can reshape the rest of the day. A task finished
// comes back named, so its row can be seen off.
func (m Model) logged(do func(*client.Client) (string, int64, error)) tea.Cmd {
	c := m.client
	if m.tookFrom == modeDay {
		return func() tea.Msg {
			note, finished, err := do(c)
			if err != nil {
				return dayMsg{err: err}
			}
			resp, err := c.Today("")
			return dayActedMsg{note: note, finished: finished, resp: resp, err: err}
		}
	}
	return func() tea.Msg {
		note, finished, err := do(c)
		return actedMsg{note: note, finished: finished, err: err}
	}
}
