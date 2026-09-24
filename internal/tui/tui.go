// Package tui is the terminal view of the list. It is a thin client like the
// CLI: it renders what the daemon returns and never decides anything about
// ordering or enrichment itself. What it adds is grouping, an explanation of
// why each task sits where it does, and the filters that answer "what can I
// do right now" without a conversation.
package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
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

// A finished task stays where it was, ticked, for long enough to be seen
// going, rather than vanishing and leaving the rows to jump under the eye.
const linger = 1200 * time.Millisecond

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
	modeLeft
	modeNotes
	modePick
	modeSearch
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

	tasks  []api.Task
	today  *api.Tally
	rows   []row
	cursor int
	filter int
	query  string

	mode      mode
	input     textinput.Model
	related   *api.RelatedResponse
	candidate int
	confirm   int64

	day       *api.TodayResponse
	dayCursor int

	edit       api.Task
	editFrom   mode
	field      string
	helpFrom   mode
	notes      textarea.Model
	noteScroll int

	pickFrom   []api.Task
	pickTicked map[int64]bool
	pickCursor int

	took     api.Task
	tookFrom mode
	working  bool
	worked   int

	message string
	failure string
	pending bool

	// leaving is the task just finished, drawn ticked where it was and no
	// longer something a key acts on. While it lingers, what comes back from
	// the daemon waits, the day's in held, and is laid out once it has gone.
	leaving   int64
	lingering bool
	held      *api.TodayResponse

	width, height int
}

func New(c *client.Client) Model {
	in := textinput.New()
	in.Placeholder = addPlaceholder
	in.CharLimit = 500
	return Model{client: c, input: in, notes: newNotesEditor()}
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
// move a recurring task to a different section. finished names a task that
// has just been completed.
type actedMsg struct {
	note     string
	finished int64
	err      error
}

type tickMsg time.Time

// leftMsg is a finished task's moment on screen being over.
type leftMsg struct{}

// openedMsg is a task fetched to be opened, for a row that only names it.
type openedMsg struct {
	task *api.Task
	err  error
}

func (m Model) seeOff(id int64) (Model, tea.Cmd) {
	m.leaving, m.lingering = id, true
	return m, tea.Tick(linger, func(time.Time) tea.Msg { return leftMsg{} })
}

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
		switch m.mode {
		case modeNotes:
			m = m.sizeNotes()
		case modeField:
			m = m.sizeInput(m.field + ": ")
		case modeAdd:
			m = m.sizeInput("add: ")
		case modePick:
			m = m.sizeInput(pickPrompt)
		case modeSearch:
			m = m.sizeInput(searchPrompt)
		}
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

	case pickMsg:
		return m.applyPick(msg), nil

	case dayActedMsg:
		if msg.err != nil {
			m.failure = msg.err.Error()
			return m, nil
		}
		m.message, m.failure = msg.note, ""
		if msg.finished != 0 {
			m.held = msg.resp
			return m.seeOff(msg.finished)
		}
		return m.applyDay(msg.resp, nil), nil

	case leftMsg:
		m.lingering = false
		if m.held != nil {
			m = m.applyDay(m.held, nil)
			m.held = nil
		}
		if m.mode == modeDay {
			m.leaving = 0
			return m, nil
		}
		return m, m.fetch()

	case openedMsg:
		if msg.err != nil {
			m.failure = msg.err.Error()
			return m, nil
		}
		return m.openTask(*msg.task, modeDay), nil

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
		if msg.finished != 0 {
			return m.seeOff(msg.finished)
		}
		return m, m.fetch()

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) applyTasks(msg tasksMsg) Model {
	if m.lingering {
		return m
	}
	if msg.err != nil {
		m.failure = msg.err.Error()
		return m
	}
	m.failure = ""
	m.tasks, m.today = msg.resp.Tasks, msg.resp.Today
	m = m.relayout()
	m.leaving = 0
	m.pending = false
	for _, t := range msg.resp.Tasks {
		if !t.Enriched {
			m.pending = true
			break
		}
	}
	// An open task keeps up with the model: a field filled in behind the
	// pane appears in it rather than waiting for it to be reopened.
	if m.mode == modeEdit || m.mode == modeField || m.mode == modeNotes || m.mode == modePick {
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
	if m.lingering {
		return m
	}
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
	case modeLeft:
		return m.keyLeft(msg)
	case modeNotes:
		return m.keyNotes(msg)
	case modePick:
		return m.keyPick(msg)
	case modeSearch:
		return m.keySearch(msg)
	case modeHelp:
		m.mode, m.helpFrom = m.helpFrom, modeList
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.mode, m.helpFrom = modeHelp, modeList
	case "/":
		return m.openSearch()
	case "esc":
		if m.query != "" {
			m.query = ""
			m = m.relayout()
		}
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
		m = m.sizeInput("add: ")
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
	case "w":
		t, ok := m.selected()
		if !ok {
			return m, nil
		}
		return m.askWorked(t, 0, modeList)
	case "u":
		return m.act(func(c *client.Client, id int64) (string, error) {
			t, err := c.Undo(id)
			if err != nil {
				return "", err
			}
			if t.RemainingMinutes > 0 {
				return fmt.Sprintf("undone — %d left", t.RemainingMinutes), nil
			}
			return said("undone", t.Outcome), nil
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
		return m.openTask(t, modeList), nil
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

// selected is the task under the cursor. One on its way out is not there to
// act on, and once it has gone the cursor takes the row that slides into its
// place, even when it is a repeating task moving on to its next date.
func (m Model) selected() (api.Task, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || !m.rows[m.cursor].isTask() || m.rows[m.cursor].task.ID == m.leaving {
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

// follow keeps the cursor on the task it was on when the list is laid out
// again, since a task that moves section, to waiting say, would otherwise
// leave the cursor on whatever slid into its place, and the next key would
// act on the wrong task. A task that has left the view gives way to the row
// now where it was.
func follow(rows []row, at int, id int64, had bool) int {
	if had {
		for i, r := range rows {
			if r.isTask() && r.task.ID == id {
				return i
			}
		}
	}
	return clampToTask(rows, at)
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
