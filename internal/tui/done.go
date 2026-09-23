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

// askTook opens the "how long did it take" line in front of a completion,
// prefilled with the estimate. What was actually spent is what the estimates
// will one day be calibrated against, and a prefilled number is one key to
// confirm and plainly wrong when it is: asking after the fact is the only
// moment the answer is still remembered.
func (m Model) askTook(t api.Task, from mode) (tea.Model, tea.Cmd) {
	m.mode, m.took, m.tookFrom, m.working, m.failure = modeTook, t, from, false, ""
	m.input.Placeholder = "unknown"
	m.input.SetValue(textEstimate(t))
	m.input.CursorEnd()
	m.input.Focus()
	return m, textinput.Blink
}

// keyTook completes the task on enter, with the minutes typed or none when
// the line is empty, or for a session goes on to ask what is left. esc backs
// out without logging anything, the same as esc everywhere else in ordo.
func (m Model) keyTook(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode, m.working = m.tookFrom, false
		m.input.Blur()
		return m, nil
	case "enter":
		minutes, err := readTook(m.input.Value())
		if err != nil {
			m.failure = err.Error()
			return m, nil
		}
		if m.working {
			return m.askLeft(minutes)
		}
		m.mode, m.failure = m.tookFrom, ""
		m.input.Blur()
		id := m.took.ID
		return m, m.logged(func(c *client.Client) (string, error) { return complete(c, id, minutes) })
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func readTook(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a number of minutes; leave it empty if you do not know", value)
	}
	return n, nil
}

func complete(c *client.Client, id int64, minutes int) (string, error) {
	t, err := c.Done(id, minutes)
	if err != nil {
		return "", err
	}
	note := "done"
	if minutes > 0 {
		note = fmt.Sprintf("done in %d min", minutes)
	}
	if t.Status == "open" {
		note += " — next one " + t.Due
	}
	if waits := waitingOn(*t); waits != "" && t.Status == "done" {
		note += " — it was waiting on " + waits
	}
	return note, nil
}

func (m Model) tookFooter(width int) string {
	prompt := "minutes it took: " + m.input.View()
	if m.mode == modeLeft {
		prompt = "minutes left, 0 if it is finished: " + m.input.View()
	}
	if m.failure != "" {
		return rule(width) + "\n" + truncate(errorStyle.Render(m.failure), width) + "\n" + prompt
	}
	return rule(width) + "\n" + prompt
}
