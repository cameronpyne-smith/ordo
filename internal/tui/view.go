package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	selectStyle  = lipgloss.NewStyle().Bold(true)
	faintStyle   = lipgloss.NewStyle().Faint(true)
	overdueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	linkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	brokenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	ruleStyle    = lipgloss.NewStyle().Faint(true)
)

// The panes below the list are a fixed height so the list does not jump
// about as the selection moves between a linked task and an unlinked one.
const detailHeight = 4

func (m Model) View() string {
	if m.mode == modeHelp {
		return m.help()
	}
	width, height := m.size()
	if m.mode == modeDay || m.mode == modeTook && m.tookFrom == modeDay {
		return m.dayView(width, height)
	}
	if m.mode == modeEdit || m.mode == modeField {
		return m.editView()
	}
	if m.mode == modeNotes {
		return m.notesView(width, height)
	}

	header := m.header(width)
	footer := m.footer(width)
	detail := m.detail(width)

	listHeight := height - lipgloss.Height(header) - lipgloss.Height(detail) - lipgloss.Height(footer)
	if listHeight < 3 {
		listHeight = 3
	}
	return strings.Join([]string{header, m.list(width, listHeight), detail, footer}, "\n")
}

// size is the terminal, or a usable default before the first size message.
func (m Model) size() (int, int) {
	width, height := m.width, m.height
	if width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}
	return width, height
}

func (m Model) header(width int) string {
	left := "ordo · " + filters[m.filter].name
	right := fmt.Sprintf("%d shown", m.countTasks())
	return pad(left, right, width) + "\n" + rule(width)
}

func (m Model) countTasks() int {
	n := 0
	for _, r := range m.rows {
		if r.isTask() {
			n++
		}
	}
	return n
}

func (m Model) list(width, height int) string {
	if len(m.rows) == 0 {
		return "\n" + faintStyle.Render("  nothing here") + strings.Repeat("\n", max(height-2, 0))
	}
	start := 0
	if m.cursor >= height {
		start = m.cursor - height + 1
	}
	end := min(start+height, len(m.rows))

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(i, width))
		b.WriteString("\n")
	}
	for i := end - start; i < height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderRow(i, width int) string {
	r := m.rows[i]
	if !r.isTask() {
		return headingStyle.Render(r.heading)
	}
	t := r.task

	marker := "  "
	if i == m.cursor {
		marker = "▸ "
	}
	due := "          "
	if t.Due != "" {
		due = t.Due
	}
	if t.Overdue {
		due = overdueStyle.Render(due)
	} else {
		due = faintStyle.Render(due)
	}

	title := t.Title
	if t.Priority == "high" {
		title = "! " + title
	}
	// The link and the pending mark are the two things you scan a row for,
	// so a title too long for the terminal loses its tail rather than
	// pushing them off the edge.
	prefix := fmt.Sprintf("%s%s %s ", marker, faintStyle.Render(fmt.Sprintf("%3d", t.ID)), due)
	suffix := truncate(m.suffix(t), width/2)
	room := width - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	line := prefix + truncate(title, max(room, 12))
	if i == m.cursor {
		line = selectStyle.Render(line)
	}
	return line + suffix
}

// suffix carries the two things worth seeing on every row without opening
// it: where it points in the vault, and whether the model has been yet.
func (m Model) suffix(t api.Task) string {
	var out string
	if t.Mnemo != nil {
		style := linkStyle
		text := " [[" + t.Mnemo.Slug + "]]"
		if t.Mnemo.Missing {
			style, text = brokenStyle, text+" (missing)"
		}
		out += style.Render(text)
	}
	if !t.Enriched {
		out += faintStyle.Render(" ~")
	}
	return out
}

func (m Model) detail(width int) string {
	if m.mode == modeLink {
		return m.linkPane(width)
	}
	lines := []string{rule(width)}
	t, ok := m.selected()
	if !ok {
		lines = append(lines, "", "")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, truncate(selectStyle.Render(t.Title), width))
	lines = append(lines, truncate(faintStyle.Render(why(t)), width))
	third := ""
	switch {
	case t.Notes != "":
		third = preview(t.Notes)
	case t.Mnemo != nil:
		third = "[[" + t.Mnemo.Slug + "]] " + t.Mnemo.Title
	}
	lines = append(lines, truncate(faintStyle.Render(third), width))
	return strings.Join(lines, "\n")
}

// linkPane is the vault seen from a task: the note it points at and that
// note's neighbourhood, or the notes it could point at instead. Both states
// come from one call, so relinking a renamed note is the same gesture as
// linking for the first time.
func (m Model) linkPane(width int) string {
	lines := []string{rule(width)}
	switch {
	case m.related == nil:
		lines = append(lines, faintStyle.Render("asking mnemo…"))
	case m.related.Linked && !m.related.Missing && m.related.Note != nil:
		n := m.related.Note
		lines = append(lines, linkStyle.Render("[["+n.Slug+"]]")+" "+faintStyle.Render(n.Folder))
		lines = append(lines, truncate(n.Description, width))
		lines = append(lines, faintStyle.Render(neighbourhood(m.related)))
	default:
		if m.related.Missing {
			lines = append(lines, brokenStyle.Render("that note is gone — pick the one it became"))
		} else {
			lines = append(lines, faintStyle.Render("not linked — pick a note"))
		}
		lines = append(lines, m.candidates(width)...)
	}
	for len(lines) < detailHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines[:detailHeight], "\n")
}

func (m Model) candidates(width int) []string {
	if len(m.related.Candidates) == 0 {
		return []string{faintStyle.Render("mnemo had nothing close")}
	}
	var out []string
	for i, h := range m.related.Candidates {
		marker := "  "
		if i == m.candidate {
			marker = "▸ "
		}
		line := marker + h.Slug + faintStyle.Render("  "+h.Description)
		if i == m.candidate {
			line = selectStyle.Render(marker+h.Slug) + faintStyle.Render("  "+h.Description)
		}
		out = append(out, truncate(line, width))
	}
	return out
}

func neighbourhood(r *api.RelatedResponse) string {
	var parts []string
	if n := len(r.Similar); n > 0 {
		parts = append(parts, fmt.Sprintf("%d similar", n))
	}
	if n := len(r.Links); n > 0 {
		parts = append(parts, fmt.Sprintf("%d links", n))
	}
	if n := len(r.Backlinks); n > 0 {
		parts = append(parts, fmt.Sprintf("%d backlinks", n))
	}
	if len(parts) == 0 {
		return "no neighbours"
	}
	return strings.Join(parts, " · ") + "   u unlink · esc back"
}

func (m Model) footer(width int) string {
	switch m.mode {
	case modeAdd:
		return rule(width) + "\n" + "add: " + m.input.View()
	case modeTook:
		return m.tookFooter(width)
	case modeConfirm:
		return rule(width) + "\n" + errorStyle.Render(fmt.Sprintf("delete %d permanently? this cannot be undone  y/n", m.confirm))
	}
	var keys []string
	for i, f := range filters {
		label := f.key + " " + f.name
		if i == m.filter {
			label = selectStyle.Render(label)
		} else {
			label = faintStyle.Render(label)
		}
		keys = append(keys, label)
	}
	status := m.message
	if m.failure != "" {
		status = errorStyle.Render(m.failure)
	}
	return rule(width) + "\n" + truncate(strings.Join(keys, "  "), width) + "\n" +
		pad(faintStyle.Render("enter open · a add · d done · u undo · e enrich · l note · p pin · t today · x delete · q quit"), status, width)
}

func (m Model) help() string {
	return strings.Join([]string{
		headingStyle.Render("ordo"),
		"",
		"  The list is the daemon's order: overdue first, then by due date with",
		"  undated last, then priority, then difficulty. The sections group that",
		"  order; they never change it. The line under the list says which fields",
		"  put the selected task where it is.",
		"",
		headingStyle.Render("moving"),
		"  j k ↑ ↓   move          g G   first, last",
		"  1 – 7     filter        r     refresh now",
		"",
		headingStyle.Render("doing"),
		"  a   add a task — type the sentence, the daemon reads the rest out of it",
		"  d   done — asks how long it took, filled in with the estimate: enter",
		"      keeps it, or type the real minutes, or clear it if you do not know.",
		"      esc backs out. A recurring task stays open and moves to its next date",
		"  u   undo the last completion",
		"  e   read it with the model again; it only ever fills empty fields",
		"  x   delete permanently, after a confirmation",
		"",
		headingStyle.Render("changing a task"),
		"  enter opens the task. Inside it p and d cycle priority and difficulty,",
		"  shift steps back, and u m r t open a prefilled line for the due date,",
		"  estimate, repeat rule and title. A date is day first, 25/09/26, or",
		"  2026-09-25. Every press saves; an empty value clears the field.",
		"  A note too long for its row is shown whole underneath, j k to scroll.",
		"  n edits it over several lines: enter starts a new line, ctrl+s saves.",
		"  esc returns to the list.",
		"",
		headingStyle.Render("the day"),
		"  t   the day view: what fits in today's free time, each block placed",
		"      with the reason it landed there. p pins a task to today from the",
		"      list, and unpins it from the day. The daemon plans it, not this.",
		"",
		headingStyle.Render("mnemo"),
		"  l   the note this task points at, its neighbourhood, or the notes it",
		"      could point at. enter links the highlighted one, u cuts the link.",
		"      ordo only ever reads mnemo; nothing here changes a note.",
		"",
		faintStyle.Render("  ~ means the model has not read it yet. any key returns."),
	}, "\n")
}

func rule(width int) string { return ruleStyle.Render(strings.Repeat("─", width)) }

// pad puts right hard against the right edge, given that both sides may
// carry escape sequences that do not take up space.
// pad puts the status at the right edge. When the two do not fit, the key
// hints give way: they are always the same and there is a help screen for
// them, where the status is the only place an error is ever said.
func pad(left, right string, width int) string {
	if right == "" {
		return truncate(left, width)
	}
	room := width - lipgloss.Width(right) - 1
	if room < 1 {
		return truncate(right, width)
	}
	left = truncate(left, room)
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}
