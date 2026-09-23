package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

// sessions is a daemon that records every session it is sent and answers
// with the task as the store would leave it.
type sessions struct {
	mu   sync.Mutex
	sent []api.WorkRequest
}

func working(t *testing.T, stored api.Task) (*client.Client, *sessions) {
	t.Helper()
	got := &sessions{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mu.Lock()
		defer got.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/work"):
			var req api.WorkRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			got.sent = append(got.sent, req)
			after := stored
			if req.Left != nil && *req.Left == 0 {
				after.Status = "done"
			} else if req.Left != nil {
				after.RemainingMinutes = *req.Left
			}
			json.NewEncoder(w).Encode(after)
		case strings.HasSuffix(r.URL.Path, "/today"):
			json.NewEncoder(w).Encode(plan(nil, nil))
		default:
			json.NewEncoder(w).Encode(api.ListResponse{Tasks: []api.Task{stored}, Count: 1})
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, ""), got
}

// From the day, both lines come prefilled — the block's length, then what
// was left less that — so an hour done as planned is two presses of enter.
func TestWorkedFromTheDayOffersTheBlockAndWhatIsLeft(t *testing.T) {
	b := block("07:00", "08:00", "Write the report", func(b *api.Block) {
		b.Minutes, b.Left = 60, 300
		b.Task.EstimateMinutes = 300
	})
	c, got := working(t, b.Task)
	m := New(c)
	m.mode = modeDay
	m = m.applyDay(plan([]api.Block{b}, nil), nil)

	m, _ = press(t, m, "w")
	if m.mode != modeTook || m.input.Value() != "60" {
		t.Fatalf("mode = %v, line = %q; want the block's 60 offered", m.mode, m.input.Value())
	}
	m, _ = press(t, m, "enter")
	if m.mode != modeLeft || m.input.Value() != "240" {
		t.Fatalf("mode = %v, line = %q; want 240 left offered", m.mode, m.input.Value())
	}
	m.width, m.height = 100, 30
	if frame := m.View(); !strings.Contains(frame, "07:00-08:00") || !strings.Contains(frame, "minutes left") {
		t.Errorf("want the day with the second line under it:\n%s", frame)
	}
	m, cmd := press(t, m, "enter")
	m = run(t, m, cmd)
	if len(got.sent) != 1 || got.sent[0].Minutes != 60 || got.sent[0].Left == nil || *got.sent[0].Left != 240 {
		t.Fatalf("sent %+v, want 60 minutes with 240 left", got.sent)
	}
	if m.mode != modeDay || m.message != "60 min logged — 240 left" {
		t.Fatalf("mode = %v, message = %q", m.mode, m.message)
	}
}

// Time spent is not progress, so what is left can be said outright, and 0
// finishes the task.
func TestWorkedCanSayWhatIsReallyLeft(t *testing.T) {
	report := task(4, "Write the report", func(x *api.Task) { x.EstimateMinutes = 300; x.RemainingMinutes = 120 })
	c, got := working(t, report)
	m := listOf(c, report)

	m, _ = press(t, m, "w")
	if m.input.Value() != "" {
		t.Fatalf("line = %q, want nothing offered from the list", m.input.Value())
	}
	m.input.SetValue("90")
	m, _ = press(t, m, "enter")
	if m.input.Value() != "30" {
		t.Fatalf("left = %q, want 120 less 90", m.input.Value())
	}
	m.input.SetValue("0")
	m, cmd := press(t, m, "enter")
	m = run(t, m, cmd)
	if len(got.sent) != 1 || *got.sent[0].Left != 0 || m.message != "done in 90 min" {
		t.Fatalf("sent %+v, message %q; want it finished", got.sent, m.message)
	}
}

// A repeating task is done in one go, so w says so and asks nothing.
func TestWorkedRefusesARepeatingTask(t *testing.T) {
	bins := task(3, "Bins", func(x *api.Task) { x.Recur = &api.Recur{Kind: "every", Rule: "weekly on tue"} })
	c, got := working(t, bins)
	m := listOf(c, bins)

	m, cmd := press(t, m, "w")
	if cmd != nil || m.mode != modeList || m.failure == "" {
		t.Fatalf("mode = %v, failure = %q; want a refusal and the list", m.mode, m.failure)
	}
	if len(got.sent) != 0 {
		t.Fatalf("sent %+v, want nothing", got.sent)
	}
}

// esc on either line logs nothing.
func TestEscapeLogsNoSession(t *testing.T) {
	report := task(4, "Write the report", func(x *api.Task) { x.EstimateMinutes = 300 })
	c, got := working(t, report)
	m := listOf(c, report)

	m, _ = press(t, m, "w")
	m, _ = press(t, m, "enter")
	m, cmd := press(t, m, "esc")
	if cmd != nil {
		cmd()
	}
	if len(got.sent) != 0 || m.mode != modeList || m.working {
		t.Fatalf("sent %+v, mode = %v; want nothing sent and the list back", got.sent, m.mode)
	}
	m, _ = press(t, m, "d")
	if m.working {
		t.Fatal("d after an abandoned w still thinks it is logging a session")
	}
}

// A piece says on the day what it is a piece of.
func TestADayRowSaysWhatAPieceIsOf(t *testing.T) {
	m := New(nil)
	m.mode = modeDay
	m.width, m.height = 100, 30
	m = m.applyDay(plan([]api.Block{
		block("07:00", "08:00", "Write the report", func(b *api.Block) { b.Minutes, b.Left = 60, 240 }),
	}, nil), nil)
	if frame := m.View(); !strings.Contains(frame, "60 min of 240 left") {
		t.Fatalf("want the piece named:\n%s", frame)
	}
}

func TestWhyCountsTimeForAQuickWin(t *testing.T) {
	for _, c := range []struct {
		task api.Task
		want []string
		not  string
	}{
		{task(1, "x", func(x *api.Task) { x.Difficulty = "high"; x.EstimateMinutes = 10 }), []string{"high difficulty", "quick win"}, ""},
		{task(1, "x", func(x *api.Task) { x.Difficulty = "high"; x.EstimateMinutes = 300; x.RemainingMinutes = 20 }),
			[]string{"quick win", "about 300 min, 20 left"}, ""},
		{task(1, "x", func(x *api.Task) { x.EstimateMinutes = 300; x.RemainingMinutes = 240 }), []string{"about 300 min, 240 left"}, "quick win"},
	} {
		got := why(c.task)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("why = %q, want %q", got, w)
			}
		}
		if c.not != "" && strings.Contains(got, c.not) {
			t.Errorf("why = %q, want no %q", got, c.not)
		}
	}
}
