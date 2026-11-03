package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

// The chooser narrows the list to one tag or one note. Both are filters
// like the numbered ones, but with a value, so instead of a key each they
// share a picker over what is actually in use: the tags some task carries,
// the notes some open task points at. Choosing the one already active
// clears it, the way a numbered key toggles.

const (
	chooseTag  = "tag"
	chooseNote = "note"
)

// choice is one thing on offer: what the filter is set to, how it reads,
// and how many open tasks it would show.
type choice struct {
	value  string
	label  string
	detail string
	count  int
}

type chooseMsg struct {
	choices []choice
	err     error
}

func (m Model) openChoose(kind string) (tea.Model, tea.Cmd) {
	m.mode, m.chooseKind, m.choices, m.chooseCursor = modeChoose, kind, nil, 0
	m.failure, m.message = "", ""
	m = m.sizeInput(kind + ": ")
	m.input.Placeholder = "type to narrow"
	m.input.SetValue("")
	m.input.Focus()
	c := m.client
	return m, tea.Batch(textinput.Blink, func() tea.Msg {
		if kind == chooseTag {
			resp, err := c.Tags()
			if err != nil {
				return chooseMsg{err: err}
			}
			return chooseMsg{choices: tagChoices(resp.Tags)}
		}
		resp, err := c.List(client.Filter{})
		if err != nil {
			return chooseMsg{err: err}
		}
		return chooseMsg{choices: noteChoices(resp.Tasks)}
	})
}

func tagChoices(tags []api.TagCount) []choice {
	out := []choice{}
	for _, t := range tags {
		out = append(out, choice{value: t.Tag, label: colourAt(t.Colour).Render("#" + t.Tag), count: t.Open})
	}
	return out
}

// noteChoices is every note the open tasks point at, with the title each
// task remembered for it, since a slug alone is not always recognisable.
func noteChoices(tasks []api.Task) []choice {
	byslug := map[string]*choice{}
	for _, t := range tasks {
		if t.Mnemo == nil {
			continue
		}
		c, ok := byslug[t.Mnemo.Slug]
		if !ok {
			c = &choice{value: t.Mnemo.Slug, label: noteBadge(t.Mnemo.Slug), detail: t.Mnemo.Title}
			byslug[t.Mnemo.Slug] = c
		}
		c.count++
	}
	out := []choice{}
	for _, c := range byslug {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].value < out[j].value })
	return out
}

func (m Model) applyChoose(msg chooseMsg) Model {
	if msg.err != nil {
		m.failure = msg.err.Error()
		return m
	}
	m.choices, m.chooseCursor = msg.choices, 0
	return m
}

// current is the value the active filter already has for what is being
// chosen.
func (m Model) current() string {
	if m.chooseKind == chooseTag {
		return m.active.Tag
	}
	return m.active.Note
}

func (m Model) chooseShown() []choice {
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if query == "" {
		return m.choices
	}
	var out []choice
	for _, c := range m.choices {
		if strings.Contains(strings.ToLower(c.value+" "+c.detail), query) {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) keyChoose(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	shown := m.chooseShown()
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "up":
		m.chooseCursor = max(m.chooseCursor-1, 0)
		return m, nil
	case "down":
		m.chooseCursor = min(m.chooseCursor+1, max(len(shown)-1, 0))
		return m, nil
	case "enter":
		if m.chooseCursor >= len(shown) {
			return m, nil
		}
		chosen := shown[m.chooseCursor].value
		if chosen == m.current() {
			chosen = ""
		}
		if m.chooseKind == chooseTag {
			m.active.Tag = chosen
		} else {
			m.active.Note = chosen
		}
		m.mode, m.cursor = modeList, 0
		m.input.Blur()
		return m, m.fetch()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.chooseCursor = min(m.chooseCursor, max(len(m.chooseShown())-1, 0))
	return m, cmd
}

func (m Model) chooseView() string {
	width, height := m.size()
	header := m.header(width)
	footer := m.chooseFooter(width)
	lines := []string{"", "  " + m.chooseKind + ": " + m.input.View(), ""}
	room := max(height-lineCount(header)-lineCount(footer)-len(lines), 1)

	shown := m.chooseShown()
	switch {
	case m.choices == nil:
		lines = append(lines, faintStyle.Render("  asking the daemon…"))
	case len(m.choices) == 0 && m.chooseKind == chooseTag:
		lines = append(lines, faintStyle.Render("  no tags in use yet: open a task and press # to give it one"))
	case len(m.choices) == 0:
		lines = append(lines, faintStyle.Render("  no open task points at a note"))
	case len(shown) == 0:
		lines = append(lines, faintStyle.Render("  nothing matches"))
	default:
		start := 0
		if m.chooseCursor >= room {
			start = m.chooseCursor - room + 1
		}
		for i := start; i < min(start+room, len(shown)); i++ {
			c := shown[i]
			marker, box := "  ", "   "
			if i == m.chooseCursor {
				marker = "▸ "
			}
			if c.value == m.current() {
				box = "[x]"
			}
			count := fmt.Sprintf("%d open", c.count)
			if c.count == 1 {
				count = "1 open"
			}
			row := marker + box + " " + c.label
			if c.detail != "" {
				row += faintStyle.Render("  " + c.detail)
			}
			row += faintStyle.Render("  " + count)
			if i == m.chooseCursor {
				row = selectStyle.Render(marker+box) + " " + c.label + faintStyle.Render("  "+strings.TrimSpace(c.detail+"  "+count))
			}
			lines = append(lines, truncate(row, width))
		}
	}
	for len(lines) < room+3 {
		lines = append(lines, "")
	}
	return strings.Join(append(append([]string{header}, lines...), footer), "\n")
}

func (m Model) chooseFooter(width int) string {
	hint := "enter narrow to it"
	if m.current() != "" {
		hint = "enter narrow to it, or clear the one ticked"
	}
	keys := faintStyle.Render("↑ ↓ move · " + hint + " · esc back")
	if m.failure != "" {
		return rule(width) + "\n" + pad(keys, errorStyle.Render(m.failure), width)
	}
	return rule(width) + "\n" + truncate(keys, width)
}
