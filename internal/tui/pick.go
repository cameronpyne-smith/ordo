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

const pickPrompt = "waits on: "

// pickMsg is the open list coming back for the picker to choose from.
type pickMsg struct {
	resp *api.ListResponse
	err  error
}

// openPick asks for the open tasks and shows what this one waits on as a
// list to tick, which is easier than remembering ids.
func (m Model) openPick() (tea.Model, tea.Cmd) {
	m.mode, m.failure, m.pickFrom, m.pickCursor = modePick, "", nil, 0
	m.pickTicked = map[int64]bool{}
	for _, d := range m.edit.BlockedBy {
		m.pickTicked[d.ID] = true
	}
	m = m.sizeInput(pickPrompt)
	m.input.Placeholder = "type to filter by title or id"
	m.input.SetValue("")
	m.input.Focus()
	c := m.client
	return m, tea.Batch(textinput.Blink, func() tea.Msg {
		resp, err := c.List(client.Filter{})
		return pickMsg{resp: resp, err: err}
	})
}

// applyPick keeps only what the task could wait on: open one-off tasks
// other than itself, and nothing that already waits on it, since that would
// be a loop. A blocker already done stays on offer so it can be kept or
// dropped. What is ticked comes first, so the current list is on screen.
func (m Model) applyPick(msg pickMsg) Model {
	if msg.err != nil {
		m.failure = msg.err.Error()
		return m
	}
	downstream := waitingOnTransitively(m.edit.ID, msg.resp.Tasks)
	var ticked, rest []api.Task
	for _, d := range m.edit.BlockedBy {
		if d.Done {
			ticked = append(ticked, api.Task{ID: d.ID, Title: d.Title, Status: "done"})
		}
	}
	for _, t := range msg.resp.Tasks {
		if t.ID == m.edit.ID || t.Recur != nil || downstream[t.ID] {
			continue
		}
		if m.pickTicked[t.ID] {
			ticked = append(ticked, t)
		} else {
			rest = append(rest, t)
		}
	}
	m.pickFrom = append(ticked, rest...)
	m.pickCursor = 0
	return m
}

// waitingOnTransitively is every task that waits on id, directly or through
// others.
func waitingOnTransitively(id int64, tasks []api.Task) map[int64]bool {
	waiters := map[int64][]int64{}
	for _, t := range tasks {
		for _, d := range t.BlockedBy {
			waiters[d.ID] = append(waiters[d.ID], t.ID)
		}
	}
	out := map[int64]bool{}
	queue := []int64{id}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, w := range waiters[at] {
			if !out[w] {
				out[w] = true
				queue = append(queue, w)
			}
		}
	}
	return out
}

// pickShown is the offer narrowed by what has been typed: a number matches
// the start of an id, and words match anywhere in the title.
func (m Model) pickShown() []api.Task {
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if query == "" {
		return m.pickFrom
	}
	_, numeric := strconv.Atoi(query)
	var out []api.Task
	for _, t := range m.pickFrom {
		if numeric == nil && strings.HasPrefix(strconv.FormatInt(t.ID, 10), query) {
			out = append(out, t)
			continue
		}
		title, all := strings.ToLower(t.Title), true
		for _, word := range strings.Fields(query) {
			if !strings.Contains(title, word) {
				all = false
				break
			}
		}
		if all {
			out = append(out, t)
		}
	}
	return out
}

func (m Model) keyPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	shown := m.pickShown()
	switch msg.String() {
	case "esc":
		m.mode = modeEdit
		m.input.Blur()
		return m, nil
	case "up":
		m.pickCursor = max(m.pickCursor-1, 0)
		return m, nil
	case "down":
		m.pickCursor = min(m.pickCursor+1, max(len(shown)-1, 0))
		return m, nil
	case " ":
		if m.pickCursor < len(shown) {
			id := shown[m.pickCursor].ID
			m.pickTicked[id] = !m.pickTicked[id]
		}
		return m, nil
	case "enter":
		if m.pickFrom == nil {
			return m, nil
		}
		m.mode = modeEdit
		m.input.Blur()
		ids := []int64{}
		for _, t := range m.pickFrom {
			if m.pickTicked[t.ID] {
				ids = append(ids, t.ID)
			}
		}
		note := "waits on nothing"
		if len(ids) > 0 {
			parts := make([]string, 0, len(ids))
			for _, id := range ids {
				parts = append(parts, strconv.FormatInt(id, 10))
			}
			note = "waits on " + strings.Join(parts, ", ")
		}
		return m.send(api.EditRequest{BlockedBy: &ids}, note)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.pickCursor = min(m.pickCursor, max(len(m.pickShown())-1, 0))
	return m, cmd
}

func (m Model) pickView() string {
	width, height := m.size()
	header := m.editHeader(width)
	footer := m.pickFooter(width)
	lines := []string{"", "  " + pickPrompt + m.input.View(), ""}
	room := max(height-lineCount(header)-lineCount(footer)-len(lines), 1)

	shown := m.pickShown()
	switch {
	case m.pickFrom == nil:
		lines = append(lines, faintStyle.Render("  reading the list…"))
	case len(shown) == 0:
		lines = append(lines, faintStyle.Render("  nothing matches"))
	default:
		start := 0
		if m.pickCursor >= room {
			start = m.pickCursor - room + 1
		}
		for i := start; i < min(start+room, len(shown)); i++ {
			t := shown[i]
			marker, box := "  ", "[ ]"
			if i == m.pickCursor {
				marker = "▸ "
			}
			if m.pickTicked[t.ID] {
				box = "[x]"
			}
			title := t.Title
			if t.Status == "done" {
				title += " ✓"
			}
			row := fmt.Sprintf("%s%s %s %s", marker, box, faintStyle.Render(fmt.Sprintf("%3d", t.ID)), title)
			if i == m.pickCursor {
				row = selectStyle.Render(row)
			}
			lines = append(lines, truncate(row, width))
		}
	}
	for len(lines) < room+3 {
		lines = append(lines, "")
	}
	return strings.Join(append(append([]string{header}, lines...), footer), "\n")
}

func (m Model) pickFooter(width int) string {
	keys := faintStyle.Render("↑ ↓ move · space tick · enter save · esc cancel")
	if m.failure != "" {
		return rule(width) + "\n" + pad(keys, errorStyle.Render(m.failure), width)
	}
	return rule(width) + "\n" + truncate(keys, width)
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }
