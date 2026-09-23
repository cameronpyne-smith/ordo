package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

func blockedBy(deps ...api.Dep) func(*api.Task) {
	return func(t *api.Task) {
		t.BlockedBy = deps
		for _, d := range deps {
			if !d.Done {
				t.Blocked = true
			}
		}
	}
}

// Everything you could pick up now comes first; what waits, on another task
// or its start date, is its own section, each row saying why.
func TestWaitingTasksHaveTheirOwnSection(t *testing.T) {
	m := listOf(nil,
		task(1, "Re-rate the skills matrix", due("2099-10-15"), blockedBy(api.Dep{ID: 10, Title: "Read"})),
		task(2, "Plan the next block", func(x *api.Task) { x.Start = "2099-01-01" }),
		task(3, "Read AFML chapter 11"),
		task(4, "Released", blockedBy(api.Dep{ID: 9, Title: "Done already", Done: true})),
	)
	var headings []string
	for _, r := range m.rows {
		if !r.isTask() {
			headings = append(headings, r.heading)
		}
	}
	if strings.Join(headings, "|") != "someday|waiting" {
		t.Fatalf("headings = %v, want someday then waiting", headings)
	}
	m.width, m.height = 100, 20
	view := m.View()
	for _, want := range []string{"waits on 10", "from 2099-01-01"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q\n%s", want, view)
		}
	}
	for _, r := range m.rows {
		if r.isTask() && r.task.ID == 4 && sectionOf(r.task) != sectionSomeday {
			t.Errorf("released task is in %q, want it back with what can be started", sectionOf(r.task))
		}
	}
}

// A deadline passed down reads as the row's date, and the row and the line
// under the list both say whose it is.
func TestAPassedDownDeadlineSaysWhoseItIs(t *testing.T) {
	read := task(10, "Read AFML chapter 11", func(x *api.Task) { x.EffectiveDue, x.DueFor = "2099-10-13", 12 })
	m := listOf(nil, read)
	m.width, m.height = 100, 20
	view := m.View()
	if !strings.Contains(view, "2099-10-13") || !strings.Contains(view, "for 12") {
		t.Errorf("want the passed-down date and whose it is on the row:\n%s", view)
	}
	if !strings.Contains(why(read), "so 12 can follow in time") {
		t.Errorf("why = %q, want it to name the task waiting", why(read))
	}
}

func TestEditViewShowsBothEndsOfItsDependencies(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.width, m.height = 100, 30
	m.edit = task(11, "Read AFML chapter 7",
		blockedBy(api.Dep{ID: 13, Title: "Download the papers"}, api.Dep{ID: 10, Title: "Read AFML chapter 11", Done: true}),
		func(x *api.Task) { x.Blocks = []api.Dep{{ID: 12, Title: "Re-rate the skills matrix"}} })
	view := m.View()
	for _, want := range []string{"waits on", "13 Download the papers", "10 Read AFML chapter 11 ✓",
		"blocks", "12 Re-rate the skills matrix", "start"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q\n%s", want, view)
		}
	}
}

// picking is a daemon with a list to offer that records the edit it is sent.
func picking(t *testing.T, tasks []api.Task) (*client.Client, *api.EditRequest) {
	t.Helper()
	var got api.EditRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/edit") {
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(tasks[0])
			return
		}
		json.NewEncoder(w).Encode(api.ListResponse{Tasks: tasks, Count: len(tasks)})
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, ""), &got
}

// The picker offers only what the task could wait on, with what it waits on
// now ticked and first; typing a number finds an id, space ticks, and enter
// sends the whole list.
func TestThePickerSetsWhatATaskWaitsOn(t *testing.T) {
	self := task(3, "Re-rate the skills matrix",
		blockedBy(api.Dep{ID: 1, Title: "Read AFML chapter 11"}, api.Dep{ID: 9, Title: "Buy the book", Done: true}))
	list := []api.Task{
		self,
		task(1, "Read AFML chapter 11"),
		task(2, "Read CASI chapters 1-2"),
		task(4, "Daily drill", func(x *api.Task) { x.Recur = &api.Recur{Kind: "every", Rule: "daily"} }),
		task(5, "Write it up", blockedBy(api.Dep{ID: 3})),
		task(6, "Send it", blockedBy(api.Dep{ID: 5})),
		task(21, "Read Grinold & Kahn"),
	}
	c, got := picking(t, list)
	m := New(c)
	m.width, m.height = 100, 30
	m = m.openTask(self, modeList)

	m, cmd := press(t, m, "w")
	if m.mode != modePick {
		t.Fatalf("mode = %v, want the picker", m.mode)
	}
	m = m.applyPick(pickResult(t, cmd))
	var offered []int64
	for _, o := range m.pickFrom {
		offered = append(offered, o.ID)
	}
	if want := []int64{9, 1, 2, 21}; !equalIDs(offered, want) {
		t.Fatalf("offered %v, want %v: ticked first, nothing repeating, nothing that waits on it", offered, want)
	}
	if view := m.View(); !strings.Contains(view, "[x]") || !strings.Contains(view, "Buy the book ✓") {
		t.Errorf("want the current list ticked:\n%s", view)
	}

	m, _ = press(t, m, "2")
	if shown := m.pickShown(); len(shown) != 2 || shown[0].ID != 2 || shown[1].ID != 21 {
		t.Fatalf("shown = %+v, want the ids starting 2", shown)
	}
	m, _ = press(t, m, " ")
	m, cmd = press(t, m, "enter")
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want back on the task", m.mode)
	}
	m = run(t, m, cmd)
	if got.BlockedBy == nil || !equalIDs(*got.BlockedBy, []int64{9, 1, 2}) {
		t.Fatalf("sent %+v, want the whole list 9, 1, 2", got.BlockedBy)
	}
	if !strings.Contains(m.message, "waits on 9, 1, 2") {
		t.Errorf("message = %q, want what it now waits on", m.message)
	}
}

func TestEscapeLeavesTheListAlone(t *testing.T) {
	self := task(3, "Re-rate")
	c, got := picking(t, []api.Task{self, task(1, "Read")})
	m := New(c)
	m = m.openTask(self, modeList)
	m, cmd := press(t, m, "w")
	m = m.applyPick(pickResult(t, cmd))
	m, _ = press(t, m, " ")
	m, cmd = press(t, m, "esc")
	if m.mode != modeEdit || cmd != nil || got.BlockedBy != nil {
		t.Fatalf("mode = %v, sent %+v; want back on the task with nothing sent", m.mode, got.BlockedBy)
	}
}

// pickResult runs the fetch the picker opened with and returns its answer.
func pickResult(t *testing.T, cmd tea.Cmd) pickMsg {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("want the picker to open with a batch")
	}
	for _, c := range batch {
		if c == nil {
			continue
		}
		if msg, ok := c().(pickMsg); ok {
			return msg
		}
	}
	t.Fatal("want the picker to ask for the list")
	return pickMsg{}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Finishing a task that was still waiting is allowed, since what happened
// wins, and the note says what it had been waiting on.
func TestDoneOnAWaitingTaskSaysWhatItWaitedOn(t *testing.T) {
	stored := task(12, "Re-rate", blockedBy(api.Dep{ID: 10, Title: "Read"}))
	c, _ := completing(t, stored)
	m := listOf(c, stored)
	m, _ = press(t, m, "d")
	m, cmd := press(t, m, "enter")
	m = run(t, m, cmd)
	if !strings.Contains(m.message, "it was waiting on 10") {
		t.Fatalf("message = %q, want what it was waiting on", m.message)
	}
}
