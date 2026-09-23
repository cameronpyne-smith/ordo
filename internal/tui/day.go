package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

// The day view is the same thin client as the list: the daemon decides which
// tasks go where and why, and this only draws it. Nothing here re-plans, so
// the terminal can never show a day the CLI would disagree with.

type dayMsg struct {
	resp *api.TodayResponse
	err  error
}

// entry is one printed line of the timeline: a planned block, or a stretch
// the calendar already had. Both are drawn in time order, because that is how
// the day happens.
type entry struct {
	start  string
	end    string
	block  *api.Block
	busy   *api.Busy
	pinned bool
}

func (m Model) fetchDay() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		resp, err := c.Today("")
		return dayMsg{resp: resp, err: err}
	}
}

func dayEntries(plan *api.TodayResponse) []entry {
	if plan == nil {
		return nil
	}
	var out []entry
	for i := range plan.Blocks {
		b := &plan.Blocks[i]
		out = append(out, entry{start: b.Start, end: b.End, block: b, pinned: b.Task.PinnedOn != ""})
	}
	for i := range plan.Busy {
		b := &plan.Busy[i]
		out = append(out, entry{start: b.Start, end: b.End, busy: b})
	}
	// A stable sort keeps a block and a meeting starting at the same minute
	// in the order the daemon listed them.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].start < out[j-1].start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (m Model) dayCursorTask() (api.Task, bool) {
	entries := dayEntries(m.day)
	if m.dayCursor < 0 || m.dayCursor >= len(entries) || entries[m.dayCursor].block == nil {
		return api.Task{}, false
	}
	return entries[m.dayCursor].block.Task, true
}

func (m Model) keyDay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := dayEntries(m.day)
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "t":
		m.mode = modeList
		return m, m.fetch()
	case "?":
		m.mode, m.helpFrom = modeHelp, modeDay
		return m, nil
	case "j", "down":
		m.dayCursor = stepDay(entries, m.dayCursor, 1)
	case "k", "up":
		m.dayCursor = stepDay(entries, m.dayCursor, -1)
	case "r":
		m.message = ""
		return m, m.fetchDay()
	case "d":
		t, ok := m.dayCursorTask()
		if !ok {
			return m, nil
		}
		return m.askTook(t, modeDay)
	case "p":
		return m.actOnDay(func(c *client.Client, id int64) (string, error) {
			_, err := c.Unpin(id)
			return "unpinned; the order decides again", err
		})
	}
	return m, nil
}

// actOnDay runs a mutation against the task under the day cursor and refetches
// the plan, since completing something reshapes the rest of the day.
func (m Model) actOnDay(do func(*client.Client, int64) (string, error)) (tea.Model, tea.Cmd) {
	t, ok := m.dayCursorTask()
	if !ok {
		return m, nil
	}
	c := m.client
	return m, func() tea.Msg {
		note, err := do(c, t.ID)
		if err != nil {
			return dayMsg{err: err}
		}
		resp, err := c.Today("")
		return dayActedMsg{note: note, resp: resp, err: err}
	}
}

type dayActedMsg struct {
	note string
	resp *api.TodayResponse
	err  error
}

func stepDay(entries []entry, from, step int) int {
	for i := from + step; i >= 0 && i < len(entries); i += step {
		if entries[i].block != nil {
			return i
		}
	}
	return from
}

func clampDay(entries []entry, want int) int {
	if len(entries) == 0 {
		return 0
	}
	if want >= len(entries) {
		want = len(entries) - 1
	}
	if want < 0 {
		want = 0
	}
	if entries[want].block != nil {
		return want
	}
	if next := stepDay(entries, want, 1); entries[next].block != nil {
		return next
	}
	return stepDay(entries, want, -1)
}

func (m Model) dayView(width, height int) string {
	header := m.dayHeader(width)
	footer := m.dayFooter(width)
	detail := m.dayDetail(width)
	body := height - lipgloss.Height(header) - lipgloss.Height(detail) - lipgloss.Height(footer)
	if body < 3 {
		body = 3
	}
	return strings.Join([]string{header, m.dayBody(width, body), detail, footer}, "\n")
}

func (m Model) dayHeader(width int) string {
	if m.day == nil {
		return pad("ordo · today", "asking the daemon…", width) + "\n" + rule(width)
	}
	left := "ordo · " + m.day.Date
	right := fmt.Sprintf("%d of %d min planned", m.day.PlannedMinutes, m.day.BudgetMinutes)
	return pad(left, right, width) + "\n" + rule(width)
}

func (m Model) dayBody(width, height int) string {
	entries := dayEntries(m.day)
	if len(entries) == 0 {
		empty := "  nothing planned and nothing in the way"
		if m.day == nil {
			empty = "  …"
		}
		return "\n" + faintStyle.Render(empty) + strings.Repeat("\n", max(height-2, 0))
	}
	start := 0
	if m.dayCursor >= height {
		start = m.dayCursor - height + 1
	}
	end := min(start+height, len(entries))

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(m.dayRow(entries[i], i == m.dayCursor, width))
		b.WriteString("\n")
	}
	for i := end - start; i < height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) dayRow(e entry, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = "▸ "
	}
	span := faintStyle.Render(fmt.Sprintf("%s-%s", e.start, e.end))
	if e.busy != nil {
		what := e.busy.Summary
		if what == "" {
			what = "busy"
		}
		return truncate("  "+span+" "+brokenStyle.Render(what), width)
	}
	title := e.block.Task.Title
	if e.pinned {
		title = "* " + title
	}
	suffix := faintStyle.Render(fmt.Sprintf("  %d min", e.block.Minutes))
	prefix := marker + span + " "
	room := width - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	line := prefix + truncate(title, max(room, 12))
	if selected {
		line = selectStyle.Render(line)
	}
	return line + suffix
}

// dayDetail is the same promise the list makes: the line under the day says
// why the selected block is where it is.
func (m Model) dayDetail(width int) string {
	lines := []string{rule(width)}
	entries := dayEntries(m.day)
	if m.dayCursor < len(entries) && entries[m.dayCursor].block != nil {
		b := entries[m.dayCursor].block
		lines = append(lines, truncate(selectStyle.Render(b.Task.Title), width))
		lines = append(lines, truncate(faintStyle.Render(b.Reason), width))
	} else {
		lines = append(lines, "", "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) dayFooter(width int) string {
	if m.mode == modeTook {
		return m.tookFooter(width)
	}
	status := m.message
	if m.failure != "" {
		status = errorStyle.Render(m.failure)
	}
	if status == "" && m.day != nil {
		status = faintStyle.Render(dayNote(m.day))
	}
	keys := faintStyle.Render("j k move · d done · p unpin · r refresh · t list · ? help · q quit")
	return rule(width) + "\n" + pad(keys, status, width)
}

// dayNote says the one thing about a plan that is not visible in it: what was
// left out, and whether the calendar was actually consulted.
func dayNote(plan *api.TodayResponse) string {
	if plan.CalendarError != "" {
		return "calendar unreadable: " + plan.CalendarError
	}
	if n := len(plan.Skipped); n > 0 {
		if n == 1 {
			return "1 task did not fit"
		}
		return fmt.Sprintf("%d tasks did not fit", n)
	}
	if !plan.Calendar {
		return "no calendar feed, so nothing is busy"
	}
	return ""
}
