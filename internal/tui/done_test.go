package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

// completions is a daemon that records every completion it is sent and
// whether the plan was asked for after it.
type completions struct {
	mu      sync.Mutex
	minutes []int
	planned bool
}

func completing(t *testing.T, stored api.Task) (*client.Client, *completions) {
	t.Helper()
	got := &completions{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mu.Lock()
		defer got.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/done"):
			var req api.DoneRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			got.minutes = append(got.minutes, req.Minutes)
			done := stored
			done.Status = "done"
			json.NewEncoder(w).Encode(done)
		case strings.HasSuffix(r.URL.Path, "/today"):
			got.planned = true
			json.NewEncoder(w).Encode(plan(nil, nil))
		default:
			json.NewEncoder(w).Encode(api.ListResponse{Tasks: []api.Task{stored}, Count: 1})
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, ""), got
}

func listOf(c *client.Client, tasks ...api.Task) Model {
	m := New(c)
	m.tasks = tasks
	m.rows = layout(tasks)
	m.cursor = clampToTask(m.rows, 0)
	return m
}

func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	next, _ := m.Update(cmd())
	return next.(Model)
}

// The estimate is offered as the answer, so a task that took as long as
// expected costs one key to record.
func TestDoneOffersTheEstimateAsWhatItTook(t *testing.T) {
	office := task(6, "Make space in office", func(x *api.Task) { x.EstimateMinutes = 120 })
	c, got := completing(t, office)
	m := listOf(c, office)

	m, _ = press(t, m, "d")
	if m.mode != modeTook || m.input.Value() != "120" {
		t.Fatalf("mode = %v, line = %q; want the estimate offered", m.mode, m.input.Value())
	}
	m, cmd := press(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter completed nothing")
	}
	m = run(t, m, cmd)
	if len(got.minutes) != 1 || got.minutes[0] != 120 {
		t.Fatalf("completions = %v, want one of 120 minutes", got.minutes)
	}
	if m.mode != modeList || m.message != "done in 120 min" {
		t.Fatalf("mode = %v, message = %q", m.mode, m.message)
	}
}

func TestDoneRecordsWhatWasTyped(t *testing.T) {
	office := task(6, "Make space in office", func(x *api.Task) { x.EstimateMinutes = 120 })
	c, got := completing(t, office)
	m := listOf(c, office)

	m, _ = press(t, m, "d")
	m.input.SetValue("")
	m, _ = press(t, m, "9")
	m, _ = press(t, m, "0")
	_, cmd := press(t, m, "enter")
	cmd()
	if len(got.minutes) != 1 || got.minutes[0] != 90 {
		t.Fatalf("completions = %v, want one of 90 minutes", got.minutes)
	}
}

// Not knowing is allowed: an empty line completes the task with no time,
// which calibration skips, rather than inventing one.
func TestDoneWithNoTimeIsStillDone(t *testing.T) {
	bins := task(3, "Put the bins out")
	c, got := completing(t, bins)
	m := listOf(c, bins)

	m, _ = press(t, m, "d")
	if m.input.Value() != "" {
		t.Fatalf("line = %q, want nothing offered for a task with no estimate", m.input.Value())
	}
	_, cmd := press(t, m, "enter")
	cmd()
	if len(got.minutes) != 1 || got.minutes[0] != 0 {
		t.Fatalf("completions = %v, want one with no time", got.minutes)
	}
}

func TestDoneRefusesWhatIsNotMinutes(t *testing.T) {
	office := task(6, "Make space in office", func(x *api.Task) { x.EstimateMinutes = 120 })
	c, got := completing(t, office)
	m := listOf(c, office)

	m, _ = press(t, m, "d")
	m.input.SetValue("two hours")
	m, cmd := press(t, m, "enter")
	if cmd != nil {
		cmd()
	}
	if len(got.minutes) != 0 {
		t.Fatalf("completions = %v, want none", got.minutes)
	}
	if m.mode != modeTook || m.failure == "" {
		t.Fatalf("mode = %v, failure = %q; want the line kept open with the error", m.mode, m.failure)
	}
	m.width, m.height = 100, 30
	if frame := m.View(); !strings.Contains(frame, "two hours") || !strings.Contains(frame, "minutes it took") {
		t.Errorf("the error or the line is not on screen:\n%s", frame)
	}
}

// esc backs out the way it does everywhere else, so a stray d costs nothing.
func TestEscapeCompletesNothing(t *testing.T) {
	office := task(6, "Make space in office", func(x *api.Task) { x.EstimateMinutes = 120 })
	c, got := completing(t, office)
	m := listOf(c, office)

	m, _ = press(t, m, "d")
	m, cmd := press(t, m, "esc")
	if cmd != nil {
		cmd()
	}
	if len(got.minutes) != 0 || m.mode != modeList {
		t.Fatalf("completions = %v, mode = %v; want nothing sent and the list back", got.minutes, m.mode)
	}
}

// From the day view the prompt sits under the day, and the day is replanned
// once the task is done, since finishing one reshapes the rest.
func TestDoneFromTheDayAsksTooAndReplans(t *testing.T) {
	b := block("19:00", "19:45", "Read the paper", func(b *api.Block) { b.Task.EstimateMinutes = 45 })
	c, got := completing(t, b.Task)
	m := New(c)
	m.mode = modeDay
	m = m.applyDay(plan([]api.Block{b}, nil), nil)

	m, _ = press(t, m, "d")
	if m.mode != modeTook || m.input.Value() != "45" {
		t.Fatalf("mode = %v, line = %q", m.mode, m.input.Value())
	}
	m.width, m.height = 100, 30
	if frame := m.View(); !strings.Contains(frame, "19:00-19:45") || !strings.Contains(frame, "minutes it took") {
		t.Errorf("want the day with the line under it:\n%s", frame)
	}
	m, cmd := press(t, m, "enter")
	m = run(t, m, cmd)
	if len(got.minutes) != 1 || got.minutes[0] != 45 || !got.planned {
		t.Fatalf("completions = %v, replanned = %v", got.minutes, got.planned)
	}
	if m.mode != modeDay {
		t.Fatalf("mode = %v, want the day back", m.mode)
	}
}
