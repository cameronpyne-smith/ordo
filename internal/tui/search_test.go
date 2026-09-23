package tui

import (
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func shownIDs(m Model) []int64 {
	var out []int64
	for _, r := range m.rows {
		if r.isTask() {
			out = append(out, r.task.ID)
		}
	}
	return out
}

func typing(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = press(t, m, string(r))
	}
	return m
}

func TestWhatASearchMatches(t *testing.T) {
	sofa := task(1, "Move sofa into office", func(x *api.Task) { x.Notes = "needs the van from Dave" })
	quant := task(12, "Re-rate the skills matrix", func(x *api.Task) {
		x.Mnemo = &api.Link{Slug: "career-transition-quantitative-researcher"}
	})
	for _, tc := range []struct {
		task  api.Task
		query string
		want  bool
	}{
		{sofa, "sofa", true},
		{sofa, "OFFICE move", true},
		{sofa, "van", true},
		{sofa, "sofa wardrobe", false},
		{quant, "career", true},
		{quant, "1", true},
		{sofa, "1", true},
		{sofa, "12", false},
		{quant, "12", true},
		{quant, "  ", true},
	} {
		if got := matches(tc.task, tc.query); got != tc.want {
			t.Errorf("%q against %q = %v, want %v", tc.query, tc.task.Title, got, tc.want)
		}
	}
}

// / narrows the list as you type, and a refresh underneath keeps it narrowed;
// enter hands the keys back so the match can be acted on, and esc, then or
// later, brings the whole list back.
func TestSlashSearchesTheList(t *testing.T) {
	tasks := []api.Task{
		task(1, "Move sofa into office"),
		task(2, "Move wardrobe into office"),
		task(3, "Read AFML chapter 7", func(x *api.Task) { x.Notes = "before the office move" }),
		task(4, "Put the bins out"),
	}
	m := listOf(nil, tasks...)
	m.width, m.height = 100, 30
	m, _ = press(t, m, "/")
	if m.mode != modeSearch {
		t.Fatalf("mode = %v, want searching", m.mode)
	}
	m = typing(t, m, "office")
	if got := shownIDs(m); !equalIDs(got, []int64{1, 2, 3}) {
		t.Fatalf("shown %v, want the three that mention the office", got)
	}
	m = typing(t, m, " ward")
	if got := shownIDs(m); !equalIDs(got, []int64{2}) {
		t.Fatalf("shown %v, want only the wardrobe", got)
	}
	if sel, ok := m.selected(); !ok || sel.ID != 2 {
		t.Fatalf("selected %d, want the match under the cursor", sel.ID)
	}

	m, _ = press(t, m, "enter")
	if m.mode != modeList || m.query != "office ward" {
		t.Fatalf("mode = %v, query = %q; want the list with the search kept", m.mode, m.query)
	}
	if view := m.View(); !strings.Contains(view, "/ office ward") {
		t.Errorf("want the search named in the header:\n%s", view)
	}
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: tasks}})
	if got := shownIDs(m); !equalIDs(got, []int64{2}) {
		t.Fatalf("shown %v after a refresh, want it still narrowed", got)
	}

	m, _ = press(t, m, "esc")
	if got := shownIDs(m); len(got) != 4 || m.query != "" {
		t.Fatalf("shown %v, query %q; want esc to bring everything back", got, m.query)
	}
	if sel, _ := m.selected(); sel.ID != 2 {
		t.Errorf("selected %d, want the cursor left on the task it was on", sel.ID)
	}

	m, _ = press(t, m, "/")
	m = typing(t, m, "zzz")
	if view := m.View(); !strings.Contains(view, "nothing matches") {
		t.Errorf("want an empty search to say so:\n%s", view)
	}
	m, _ = press(t, m, "esc")
	if m.mode != modeList || len(shownIDs(m)) != 4 {
		t.Fatalf("mode = %v, shown %v; want esc while typing to drop the search", m.mode, shownIDs(m))
	}
}
