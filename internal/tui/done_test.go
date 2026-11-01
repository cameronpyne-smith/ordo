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

func TestDoneCompletesAtOnceWithNoTime(t *testing.T) {
	office := task(6, "Make space in office", func(x *api.Task) { x.EstimateMinutes = 120 })
	c, got := completing(t, office)
	m := listOf(c, office)

	m, cmd := press(t, m, "d")
	if cmd == nil {
		t.Fatal("d completed nothing")
	}
	m = run(t, m, cmd)
	if len(got.minutes) != 1 || got.minutes[0] != 0 {
		t.Fatalf("completions = %v, want one with no time", got.minutes)
	}
	if m.mode != modeList || m.message != "done" {
		t.Fatalf("mode = %v, message = %q", m.mode, m.message)
	}
}

func TestDoneFromTheDayReplans(t *testing.T) {
	b := block("19:00", "19:45", "Read the paper", func(b *api.Block) { b.Task.EstimateMinutes = 45 })
	c, got := completing(t, b.Task)
	m := New(c)
	m.mode = modeDay
	m = m.applyDay(plan([]api.Block{b}, nil), nil)

	m, cmd := press(t, m, "d")
	m = run(t, m, cmd)
	if len(got.minutes) != 1 || got.minutes[0] != 0 || !got.planned {
		t.Fatalf("completions = %v, replanned = %v", got.minutes, got.planned)
	}
	if m.mode != modeDay {
		t.Fatalf("mode = %v, want the day back", m.mode)
	}
}
