package tui

import (
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// day returns a due date n days from the daemon's today, so the tests read
// in the same terms the sections do.
func day(n int) string {
	today, err := store.ParseDate(store.Today())
	if err != nil {
		panic(err)
	}
	return today.AddDate(0, 0, n).Format(store.DateFormat)
}

func task(id int64, title string, mutate ...func(*api.Task)) api.Task {
	t := api.Task{ID: id, Title: title, Status: "open", Enriched: true}
	for _, m := range mutate {
		m(&t)
	}
	return t
}

func due(date string) func(*api.Task) {
	return func(t *api.Task) {
		t.Due = date
		if date != "" && date < store.Today() {
			t.Overdue = true
		}
	}
}

func TestSectionsFollowTheDueDate(t *testing.T) {
	cases := []struct {
		task api.Task
		want string
	}{
		{task(1, "late", due(day(-3))), sectionOverdue},
		{task(2, "now", due(day(0))), sectionToday},
		{task(3, "soon", due(day(4))), sectionThisWeek},
		{task(4, "edge", due(day(7))), sectionThisWeek},
		{task(5, "far", due(day(8))), sectionLater},
		{task(6, "undated"), sectionSomeday},
		{task(7, "finished", func(x *api.Task) { x.Status = "done" }), sectionDone},
	}
	for _, c := range cases {
		if got := sectionOf(c.task); got != c.want {
			t.Errorf("%s: section = %q, want %q", c.task.Title, got, c.want)
		}
	}
}

// A task finished today is done, not due today: the status has to win or a
// completed chore reappears at the top of the list.
func TestDoneBeatsTheDueDate(t *testing.T) {
	finished := task(1, "bins", due(day(0)), func(x *api.Task) { x.Status = "done" })
	if got := sectionOf(finished); got != sectionDone {
		t.Fatalf("section = %q, want %q", got, sectionDone)
	}
}

func TestLayoutHeadsEachSectionOnceAndKeepsTheOrder(t *testing.T) {
	rows := layout([]api.Task{
		task(1, "late", due(day(-1))),
		task(2, "also late", due(day(-1))),
		task(3, "now", due(day(0))),
		task(4, "undated"),
	})

	var got []string
	for _, r := range rows {
		if r.isTask() {
			got = append(got, "  "+r.task.Title)
			continue
		}
		got = append(got, r.heading)
	}
	want := []string{
		sectionOverdue, "  late", "  also late",
		sectionToday, "  now",
		sectionSomeday, "  undated",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows =\n%v\nwant\n%v", got, want)
	}
}

// The daemon's order is the only order. Grouping must not promote a task
// that the daemon put later.
func TestLayoutNeverReordersWithinASection(t *testing.T) {
	rows := layout([]api.Task{
		task(1, "first", due(day(2))),
		task(2, "second", due(day(3))),
		task(3, "third", due(day(2))),
	})
	var titles []string
	for _, r := range rows {
		if r.isTask() {
			titles = append(titles, r.task.Title)
		}
	}
	if strings.Join(titles, ",") != "first,second,third" {
		t.Fatalf("titles = %v", titles)
	}
}

func TestCursorSkipsHeadings(t *testing.T) {
	rows := layout([]api.Task{
		task(1, "late", due(day(-1))),
		task(2, "now", due(day(0))),
	})
	// rows: [heading, late, heading, now]
	start := clampToTask(rows, 0)
	if !rows[start].isTask() || rows[start].task.Title != "late" {
		t.Fatalf("start = %d (%+v), want the first task", start, rows[start])
	}
	next := move(rows, start, 1)
	if rows[next].task.Title != "now" {
		t.Fatalf("next = %+v, want the heading stepped over", rows[next])
	}
	if end := move(rows, next, 1); end != next {
		t.Fatalf("moved past the end to %d", end)
	}
	if back := move(rows, next, -1); back != start {
		t.Fatalf("back = %d, want %d", back, start)
	}
}

// After a refresh shortens the list the cursor has to land somewhere real,
// not on a heading and not off the end.
func TestCursorSurvivesTheListShrinking(t *testing.T) {
	m := Model{}
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
		task(1, "a", due(day(-1))), task(2, "b", due(day(0))), task(3, "c"),
	}}})
	m.cursor = len(m.rows) - 1

	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{task(1, "a", due(day(-1)))}}})
	if _, ok := m.selected(); !ok {
		t.Fatalf("cursor = %d over %d rows, want a task", m.cursor, len(m.rows))
	}
}

func TestPendingDrivesTheRefreshRate(t *testing.T) {
	m := Model{}
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{task(1, "read")}}})
	if m.pending {
		t.Fatal("an enriched list must not keep polling every two seconds")
	}
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
		task(1, "read"), task(2, "unread", func(x *api.Task) { x.Enriched = false }),
	}}})
	if !m.pending {
		t.Fatal("a list with an unread task must poll fast enough to be seen filling in")
	}
}

func TestWhyNamesTheFieldsThatPlacedIt(t *testing.T) {
	t.Run("overdue", func(t *testing.T) {
		got := why(task(1, "cv", due(day(-3)), func(x *api.Task) {
			x.Priority, x.Difficulty = "high", "low"
		}))
		for _, want := range []string{"overdue by 3 days", "high priority", "quick win"} {
			if !strings.Contains(got, want) {
				t.Errorf("why = %q, want it to mention %q", got, want)
			}
		}
	})
	t.Run("undated", func(t *testing.T) {
		got := why(task(2, "passport"))
		if !strings.Contains(got, "no due date") {
			t.Errorf("why = %q", got)
		}
	})
	t.Run("recurring and unread", func(t *testing.T) {
		got := why(task(3, "bins", due(day(1)), func(x *api.Task) {
			x.Recur = &api.Recur{Kind: "every", Rule: "weekly on tue"}
			x.Enriched = false
		}))
		for _, want := range []string{"due tomorrow", "repeats weekly on tue", "still being read"} {
			if !strings.Contains(got, want) {
				t.Errorf("why = %q, want it to mention %q", got, want)
			}
		}
	})
}

func TestViewRendersEveryMode(t *testing.T) {
	m := New(nil)
	m.width, m.height = 90, 24
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
		task(1, "Send the quant CV", due(day(-2)), func(x *api.Task) {
			x.Priority, x.Difficulty = "high", "low"
			x.Mnemo = &api.Link{Slug: "career", Title: "The quant plan"}
		}),
		task(2, "Water the plants", due(day(0)), func(x *api.Task) {
			x.Recur = &api.Recur{Kind: "after", Rule: "3d"}
		}),
		task(3, "Renew the passport", func(x *api.Task) { x.Enriched = false }),
	}}})

	for _, mode := range []mode{modeList, modeAdd, modeConfirm, modeHelp, modeLink} {
		m.mode = mode
		frame := m.View()
		if strings.TrimSpace(frame) == "" {
			t.Fatalf("mode %d rendered nothing", mode)
		}
		if got := len(strings.Split(frame, "\n")); mode != modeHelp && got > m.height {
			t.Errorf("mode %d rendered %d lines into a %d-line terminal", mode, got, m.height)
		}
	}

	m.mode = modeLink
	m.related = &api.RelatedResponse{Candidates: []api.Hit{{Slug: "career", Description: "The quant plan"}}}
	if !strings.Contains(m.View(), "career") {
		t.Fatal("the link pane should offer the candidate")
	}
}

// A terminal that has not reported its size yet still has to render, because
// the first frame goes out before the size message arrives.
func TestViewBeforeTheSizeIsKnown(t *testing.T) {
	m := New(nil)
	if strings.TrimSpace(m.View()) == "" {
		t.Fatal("rendered nothing at zero size")
	}
}

func TestRecurPhrase(t *testing.T) {
	if got := recurPhrase(api.Recur{Kind: "after", Rule: "3d"}); got != "3d after each completion" {
		t.Fatalf("got %q", got)
	}
	if got := recurPhrase(api.Recur{Kind: "every", Rule: "weekly on tue"}); got != "weekly on tue" {
		t.Fatalf("got %q", got)
	}
}
