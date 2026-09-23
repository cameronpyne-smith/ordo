// Package tui is the terminal view of the list. It is a thin client like the
// CLI: it renders what the daemon returns and never decides anything about
// ordering or enrichment itself. What it adds is grouping, an explanation of
// why each task sits where it does, and the filters that answer "what can I
// do right now" without a conversation.
package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

// While anything on screen is still unread by the model the list refreshes
// often enough that the row is seen to fill in; once everything is enriched
// there is nothing to watch for, so it drops right back.
const (
	refreshPending = 2 * time.Second
	refreshSettled = 30 * time.Second
)

type mode int

const addPlaceholder = "put the bins out every tuesday"

const (
	modeList mode = iota
	modeAdd
	modeLink
	modeConfirm
	modeHelp
	modeDay
	modeEdit
	modeField
	modeTook
)

// The filters are the whole of the TUI's cleverness, deliberately: each one
// is a question you ask the list often enough to want a key for, and each is
// a plain server-side filter rather than anything that has to think.
type namedFilter struct {
	key  string
	name string
	f    client.Filter
}

var filters = []namedFilter{
	{"1", "open", client.Filter{}},
	{"2", "overdue", client.Filter{Overdue: true}},
	{"3", "quick wins", client.Filter{Quick: true}},
	{"4", "high priority", client.Filter{Priority: "high"}},
	{"5", "linked", client.Filter{Linked: true}},
	{"6", "recurring", client.Filter{Recurring: true}},
	{"7", "done", client.Filter{Status: "done"}},
}

// row is one printed line: either a section heading or a task under it.
type row struct {
	heading string
	task    api.Task
}

func (r row) isTask() bool { return r.heading == "" }

type Model struct {
	client *client.Client

	rows   []row
	cursor int
	filter int

	mode      mode
	input     textinput.Model
	related   *api.RelatedResponse
	candidate int
	confirm   int64

	day       *api.TodayResponse
	dayCursor int

	edit     api.Task
	field    string
	helpFrom mode

	took     api.Task
	tookFrom mode

	message string
	failure string
	pending bool

	width, height int
}

func New(c *client.Client) Model {
	in := textinput.New()
	in.Placeholder = addPlaceholder
	in.CharLimit = 500
	return Model{client: c, input: in}
}

func Run(c *client.Client) error {
	_, err := tea.NewProgram(New(c), tea.WithAltScreen()).Run()
	return err
}

type tasksMsg struct {
	resp *api.ListResponse
	err  error
}

type relatedMsg struct {
	resp *api.RelatedResponse
	err  error
}

// actedMsg is any single-task mutation coming back. The note is what to show
// in the footer; the list is refetched regardless, because a completion can
// move a recurring task to a different section.
type actedMsg struct {
	note string
	err  error
}

type tickMsg time.Time

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetch(), tick(refreshPending))
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) fetch() tea.Cmd {
	f := filters[m.filter].f
	c := m.client
	return func() tea.Msg {
		resp, err := c.List(f)
		return tasksMsg{resp: resp, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		next := refreshSettled
		if m.pending {
			next = refreshPending
		}
		if m.mode == modeDay {
			return m, tea.Batch(m.fetchDay(), tick(next))
		}
		return m, tea.Batch(m.fetch(), tick(next))

	case tasksMsg:
		return m.applyTasks(msg), nil

	case dayMsg:
		return m.applyDay(msg.resp, msg.err), nil

	case dayActedMsg:
		if msg.err != nil {
			m.failure = msg.err.Error()
			return m, nil
		}
		m = m.applyDay(msg.resp, nil)
		m.message = msg.note
		return m, nil

	case editedMsg:
		if msg.err != nil {
			m.failure = msg.err.Error()
			return m, nil
		}
		m.edit, m.message, m.failure = *msg.task, msg.note, ""
		return m, m.fetch()

	case relatedMsg:
		if msg.err != nil {
			m.mode, m.failure = modeList, msg.err.Error()
			return m, nil
		}
		m.related, m.candidate, m.failure = msg.resp, 0, ""
		return m, nil

	case actedMsg:
		if msg.err != nil {
			m.failure = msg.err.Error()
			return m, nil
		}
		m.message, m.failure = msg.note, ""
		return m, m.fetch()

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) applyTasks(msg tasksMsg) Model {
	if msg.err != nil {
		m.failure = msg.err.Error()
		return m
	}
	m.failure = ""
	m.rows = layout(msg.resp.Tasks)
	m.pending = false
	for _, t := range msg.resp.Tasks {
		if !t.Enriched {
			m.pending = true
			break
		}
	}
	m.cursor = clampToTask(m.rows, m.cursor)
	// An open task keeps up with the model: a field filled in behind the
	// pane appears in it rather than waiting for it to be reopened.
	if m.mode == modeEdit || m.mode == modeField {
		for _, t := range msg.resp.Tasks {
			if t.ID == m.edit.ID {
				m.edit = t
				break
			}
		}
	}
	return m
}

func (m Model) applyDay(resp *api.TodayResponse, err error) Model {
	if err != nil {
		m.failure = err.Error()
		return m
	}
	m.failure, m.day = "", resp
	m.dayCursor = clampDay(dayEntries(resp), m.dayCursor)
	return m
}

// layout turns the daemon's ordered list into printable rows, inserting a
// heading whenever the section changes. Because the order is already right,
// a section can never be interleaved.
func layout(tasks []api.Task) []row {
	var rows []row
	var current string
	for _, section := range sectionOrder {
		for _, t := range tasks {
			if sectionOf(t) != section {
				continue
			}
			if current != section {
				rows = append(rows, row{heading: section})
				current = section
			}
			rows = append(rows, row{task: t})
		}
	}
	return rows
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeDay:
		return m.keyDay(msg)
	case modeAdd:
		return m.keyAdd(msg)
	case modeLink:
		return m.keyLink(msg)
	case modeConfirm:
		return m.keyConfirm(msg)
	case modeEdit:
		return m.keyEdit(msg)
	case modeField:
		return m.keyField(msg)
	case modeTook:
		return m.keyTook(msg)
	case modeHelp:
		m.mode, m.helpFrom = m.helpFrom, modeList
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.mode, m.helpFrom = modeHelp, modeList
	case "j", "down":
		m.cursor = move(m.rows, m.cursor, 1)
	case "k", "up":
		m.cursor = move(m.rows, m.cursor, -1)
	case "g", "home":
		m.cursor = clampToTask(m.rows, 0)
	case "G", "end":
		m.cursor = clampToTask(m.rows, len(m.rows)-1)
	case "r":
		m.message = ""
		return m, m.fetch()
	case "a":
		m.mode = modeAdd
		m.input.Placeholder = addPlaceholder
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "d":
		t, ok := m.selected()
		if !ok {
			return m, nil
		}
		return m.askTook(t, modeList)
	case "u":
		return m.act(func(c *client.Client, id int64) (string, error) {
			_, err := c.Undo(id)
			return "completion removed", err
		})
	case "e":
		return m.act(func(c *client.Client, id int64) (string, error) {
			_, err := c.Enrich(id)
			return "queued for the model", err
		})
	case "l":
		t, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.mode, m.related = modeLink, nil
		c := m.client
		return m, func() tea.Msg {
			resp, err := c.Related(t.ID)
			return relatedMsg{resp: resp, err: err}
		}
	case "enter":
		t, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.mode, m.edit, m.message = modeEdit, t, ""
	case "t":
		m.mode, m.message = modeDay, ""
		return m, m.fetchDay()
	case "p":
		return m.act(func(c *client.Client, id int64) (string, error) {
			t, err := c.Pin(id, "")
			if err != nil {
				return "", err
			}
			return "pinned to " + t.PinnedOn, nil
		})
	case "x":
		t, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.mode, m.confirm = modeConfirm, t.ID
	default:
		for i, f := range filters {
			if msg.String() == f.key {
				m.filter, m.cursor, m.message = i, 0, ""
				return m, m.fetch()
			}
		}
	}
	return m, nil
}

func (m Model) keyAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		title := m.input.Value()
		m.mode = modeList
		m.input.Blur()
		if title == "" {
			return m, nil
		}
		c := m.client
		return m, func() tea.Msg {
			_, err := c.Create(api.CreateRequest{Title: title})
			return actedMsg{note: "added — the model is reading it now", err: err}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) keyLink(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "l":
		m.mode = modeList
		return m, nil
	case "j", "down":
		if m.related != nil && m.candidate < len(m.related.Candidates)-1 {
			m.candidate++
		}
	case "k", "up":
		if m.candidate > 0 {
			m.candidate--
		}
	case "u":
		m.mode = modeList
		return m.act(func(c *client.Client, id int64) (string, error) {
			_, err := c.Unlink(id)
			return "link cut; the note is untouched", err
		})
	case "enter":
		if m.related == nil || m.candidate >= len(m.related.Candidates) {
			return m, nil
		}
		slug := m.related.Candidates[m.candidate].Slug
		m.mode = modeList
		return m.act(func(c *client.Client, id int64) (string, error) {
			_, err := c.Link(id, slug)
			return "linked to " + slug, err
		})
	}
	return m, nil
}

func (m Model) keyConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	id := m.confirm
	m.mode = modeList
	if msg.String() != "y" {
		return m, nil
	}
	c := m.client
	return m, func() tea.Msg {
		_, err := c.Delete(id)
		return actedMsg{note: "deleted permanently", err: err}
	}
}

// act runs a mutation against the selected task. Doing it this way keeps the
// key handler free of the same four lines of plumbing per binding.
func (m Model) act(do func(*client.Client, int64) (string, error)) (tea.Model, tea.Cmd) {
	t, ok := m.selected()
	if !ok {
		return m, nil
	}
	c := m.client
	return m, func() tea.Msg {
		note, err := do(c, t.ID)
		return actedMsg{note: note, err: err}
	}
}

func (m Model) selected() (api.Task, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || !m.rows[m.cursor].isTask() {
		return api.Task{}, false
	}
	return m.rows[m.cursor].task, true
}

// move steps over headings so the cursor is always on something actionable.
func move(rows []row, from, step int) int {
	for i := from + step; i >= 0 && i < len(rows); i += step {
		if rows[i].isTask() {
			return i
		}
	}
	return from
}

func clampToTask(rows []row, want int) int {
	if len(rows) == 0 {
		return 0
	}
	if want >= len(rows) {
		want = len(rows) - 1
	}
	if want < 0 {
		want = 0
	}
	if rows[want].isTask() {
		return want
	}
	if next := move(rows, want, 1); rows[next].isTask() {
		return next
	}
	return move(rows, want, -1)
}
