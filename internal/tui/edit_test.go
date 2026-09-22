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

func press(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// edited is a daemon that records the edit it was sent and answers with the
// task as it would have stored it.
func edited(t *testing.T, stored api.Task) (*client.Client, *api.EditRequest) {
	t.Helper()
	var got api.EditRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/edit") {
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(stored)
			return
		}
		json.NewEncoder(w).Encode(api.ListResponse{Tasks: []api.Task{stored}, Count: 1})
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, ""), &got
}

func TestCycleWrapsAndNeverLandsOnUnset(t *testing.T) {
	cases := []struct {
		current string
		step    int
		want    string
	}{
		{"low", 1, "normal"},
		{"normal", 1, "high"},
		{"high", 1, "low"},
		{"low", -1, "high"},
		{"high", -1, "normal"},
		// An unread task has nothing set; the first press has to start
		// somewhere rather than do nothing.
		{"", 1, "low"},
		{"", -1, "high"},
	}
	for _, c := range cases {
		if got := cycle(priorities, c.current, c.step); got != c.want {
			t.Errorf("cycle(%q, %d) = %q, want %q", c.current, c.step, got, c.want)
		}
	}
}

func TestEditRequestReadsEachField(t *testing.T) {
	req, err := editRequest("due", "2026-10-01")
	if err != nil || req.Due == nil || *req.Due != "2026-10-01" {
		t.Fatalf("due: %+v %v", req, err)
	}
	// An empty value is how a field is cleared, so it has to reach the
	// daemon as a present-but-empty edit rather than an absent one.
	if req, err := editRequest("due", ""); err != nil || req.Due == nil || *req.Due != "" {
		t.Fatalf("clearing due: %+v %v", req, err)
	}
	if req, err := editRequest("estimate", "45"); err != nil || req.EstimateMinutes == nil || *req.EstimateMinutes != 45 {
		t.Fatalf("estimate: %+v %v", req, err)
	}
	if _, err := editRequest("estimate", "half an hour"); err == nil {
		t.Error("estimate should refuse words")
	}
	req, err = editRequest("repeats", "every weekly on tue")
	if err != nil || req.RecurKind == nil || *req.RecurKind != "every" || *req.RecurRule != "weekly on tue" {
		t.Fatalf("repeats: %+v %v", req, err)
	}
	if req, err := editRequest("repeats", ""); err != nil || *req.RecurKind != "" || *req.RecurRule != "" {
		t.Fatalf("clearing repeats: %+v %v", req, err)
	}
	if _, err := editRequest("repeats", "fortnightly"); err == nil {
		t.Error("repeats should refuse a rule with no kind")
	}
	if _, err := editRequest("repeats", "every"); err == nil {
		t.Error("repeats should refuse a kind with no rule")
	}
	if _, err := editRequest("title", ""); err == nil {
		t.Error("a task must keep a title")
	}
}

func TestEnterOpensTheTaskUnderTheCursor(t *testing.T) {
	m := New(nil)
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
		task(1, "first"), task(2, "second"),
	}}})
	m.cursor = move(m.rows, m.cursor, 1)

	m, _ = press(t, m, "enter")
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want modeEdit", m.mode)
	}
	if m.edit.ID != 2 {
		t.Fatalf("opened #%d, want the task under the cursor", m.edit.ID)
	}
	m, _ = press(t, m, "esc")
	if m.mode != modeList {
		t.Fatalf("esc left mode = %v", m.mode)
	}
}

func TestCyclingSendsTheNextValueAndShowsWhatWasStored(t *testing.T) {
	stored := task(1, "Move sofa into office", func(x *api.Task) { x.Priority = "normal" })
	c, got := edited(t, stored)

	m := New(c)
	m.mode = modeEdit
	m.edit = task(1, "Move sofa into office", func(x *api.Task) { x.Priority = "low" })

	m, cmd := press(t, m, "p")
	if cmd == nil {
		t.Fatal("p sent nothing")
	}
	msg := cmd()
	if got.Priority == nil || *got.Priority != "normal" {
		t.Fatalf("daemon was sent %+v, want priority normal", got)
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	if m.edit.Priority != "normal" {
		t.Fatalf("pane shows %q, want what the daemon stored", m.edit.Priority)
	}
	if m.message != "priority normal" {
		t.Fatalf("message = %q", m.message)
	}
}

func TestShiftCyclesBackwards(t *testing.T) {
	c, got := edited(t, task(1, "x"))
	m := New(c)
	m.mode = modeEdit
	m.edit = task(1, "x", func(x *api.Task) { x.Difficulty = "medium" })

	_, cmd := press(t, m, "D")
	if cmd == nil {
		t.Fatal("D sent nothing")
	}
	cmd()
	if got.Difficulty == nil || *got.Difficulty != "low" {
		t.Fatalf("sent %+v, want difficulty low", got)
	}
}

// A due date is nearly always a correction, so the line opens on what is
// already there rather than empty.
func TestEditingAFieldPrefillsWhatIsThere(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.edit = task(1, "x", due("2026-10-01"))

	m, _ = press(t, m, "u")
	if m.mode != modeField || m.field != "due" {
		t.Fatalf("mode = %v field = %q", m.mode, m.field)
	}
	if m.input.Value() != "2026-10-01" {
		t.Fatalf("input = %q, want the current due date", m.input.Value())
	}
	m, _ = press(t, m, "esc")
	if m.mode != modeEdit {
		t.Fatalf("esc from the line left mode = %v", m.mode)
	}
}

func TestClearingAFieldSaysSoAndSendsAnEmptyEdit(t *testing.T) {
	c, got := edited(t, task(1, "x"))
	m := New(c)
	m.mode = modeEdit
	m.edit = task(1, "x", due("2026-10-01"))

	m, _ = press(t, m, "u")
	m.input.SetValue("")
	m, cmd := press(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter sent nothing")
	}
	cmd()
	if got.Due == nil || *got.Due != "" {
		t.Fatalf("sent %+v, want an empty due", got)
	}
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want the pane back", m.mode)
	}
}

func TestABadValueIsReportedAndSendsNothing(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.edit = task(1, "x")

	m, _ = press(t, m, "m")
	m.input.SetValue("ages")
	m, cmd := press(t, m, "enter")
	if cmd != nil {
		t.Fatal("a bad estimate must not reach the daemon")
	}
	if !strings.Contains(m.failure, "minutes") {
		t.Fatalf("failure = %q", m.failure)
	}
}

// The model fills fields in behind whatever is on screen, and an open task
// is exactly where you would be watching for that.
func TestTheOpenTaskKeepsUpWithTheModel(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.edit = task(7, "Move sofa into office", func(x *api.Task) { x.Enriched = false })

	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
		task(7, "Move sofa into office", func(x *api.Task) { x.EstimateMinutes = 30 }),
	}}})
	if m.edit.EstimateMinutes != 30 {
		t.Fatalf("estimate = %d, want the pane to have caught up", m.edit.EstimateMinutes)
	}
}

func TestEditViewShowsEveryFieldAndItsKey(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.width, m.height = 90, 24
	m.edit = task(3, "Renew the passport", due("2026-10-01"), func(x *api.Task) {
		x.Priority = "high"
		x.Difficulty = "medium"
		x.EstimateMinutes = 60
		x.Recur = &api.Recur{Kind: "every", Rule: "monthly on 1"}
		x.Mnemo = &api.Link{Slug: "latent", Title: "the project"}
	})

	view := m.View()
	for _, want := range []string{
		"#3", "Renew the passport", "2026-10-01", "high", "medium", "60 min",
		"monthly on 1", "[[latent]]", "esc back",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q\n%s", want, view)
		}
	}
	for _, f := range fields {
		if !strings.Contains(view, f.name) {
			t.Errorf("view is missing the %s row", f.name)
		}
	}
}

// An empty field has to read as empty rather than as a blank the eye skips.
func TestEditViewMarksWhatIsNotSet(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.width, m.height = 90, 24
	m.edit = task(1, "Move sofa into office", func(x *api.Task) { x.Enriched = false })

	view := m.View()
	if !strings.Contains(view, "—") {
		t.Error("an unset field should be marked")
	}
	if !strings.Contains(view, "still reading") {
		t.Errorf("an unread task should say so\n%s", view)
	}
}

func TestHelpReturnsWhereItCameFrom(t *testing.T) {
	m := New(nil)
	m.mode = modeEdit
	m.edit = task(1, "x")

	m, _ = press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("mode = %v, want help", m.mode)
	}
	m, _ = press(t, m, "j")
	if m.mode != modeEdit {
		t.Fatalf("mode = %v, want the pane back rather than the list", m.mode)
	}
}
