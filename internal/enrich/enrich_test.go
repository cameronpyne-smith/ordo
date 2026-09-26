package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

// stub answers with whatever the test wants, recording the prompt it saw.
type stub struct {
	mu      sync.Mutex
	answers []string
	errs    []error
	prompts []string
	calls   int
}

func (s *stub) Model() string { return "stub" }

func (s *stub) Chat(ctx context.Context, system, prompt string, schema any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.calls
	s.calls++
	s.prompts = append(s.prompts, prompt)
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i < len(s.answers) {
		return json.RawMessage(s.answers[i]), nil
	}
	return json.RawMessage(`{"difficulty":"","priority":"","due":"","recur_kind":"","recur_rule":""}`), nil
}

func (s *stub) seen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newWorker(t *testing.T, model Model) (*Worker, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, model, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func fixedNow(t *testing.T, date string) {
	t.Helper()
	when, err := time.ParseInLocation(time.RFC3339, date, store.Location)
	if err != nil {
		t.Fatalf("parsing %s: %v", date, err)
	}
	original := store.Now
	store.Now = func() time.Time { return when }
	t.Cleanup(func() { store.Now = original })
}

// waitFor polls rather than sleeping a fixed time, so the tests neither flake
// on a slow machine nor cost a fixed delay on a fast one.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWorkerFillsATask(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	model := &stub{answers: []string{
		`{"difficulty":"medium","priority":"high","due":"2026-09-25","recur_kind":"","recur_rule":""}`,
	}}
	w, st := newWorker(t, model)

	created, err := st.Create(&store.Task{Title: "Send the CV by Friday"})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Queue(created.ID)

	var got *store.Task
	waitFor(t, "the task to be enriched", func() bool {
		got, _ = st.Get(created.ID)
		return got != nil && got.EnrichedAt != nil
	})
	if got.Difficulty != store.DifficultyMedium || got.Priority != store.PriorityHigh || got.Due != "2026-09-25" {
		t.Fatalf("got %+v, want the model's fields", got)
	}
	if !strings.Contains(model.prompts[0], "Monday, 2026-09-21") {
		t.Fatalf("prompt = %q, want today's date and weekday", model.prompts[0])
	}
}

func TestWorkerAppliesRecurrence(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	model := &stub{answers: []string{
		`{"difficulty":"low","priority":"normal","due":"","recur_kind":"every","recur_rule":"Weekly On TUE"}`,
	}}
	w, st := newWorker(t, model)

	created, _ := st.Create(&store.Task{Title: "Put the bins out every tuesday"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Queue(created.ID)

	var got *store.Task
	waitFor(t, "the task to be enriched", func() bool {
		got, _ = st.Get(created.ID)
		return got != nil && got.EnrichedAt != nil
	})
	if got.RecurKind != store.RecurEvery || got.RecurRule != "weekly on tue" {
		t.Fatalf("recurrence = %s %q, want the normalised rule", got.RecurKind, got.RecurRule)
	}
	if got.Due != "2026-09-22" {
		t.Fatalf("due = %s, want the rule's first occurrence", got.Due)
	}
}

func TestWorkerRetriesThenGivesUp(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	original := backoff
	backoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { backoff = original })

	model := &stub{errs: []error{errors.New("down"), errors.New("down"), errors.New("down")}}
	w, st := newWorker(t, model)

	created, _ := st.Create(&store.Task{Title: "Bins"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Queue(created.ID)

	waitFor(t, "the worker to give up", func() bool { return model.seen() == attempts })
	got, _ := st.Get(created.ID)
	if got.EnrichedAt != nil {
		t.Fatal("a task that never succeeded should stay unenriched")
	}
}

func TestWorkerRecoversOnARetry(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	original := backoff
	backoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { backoff = original })

	model := &stub{
		errs:    []error{errors.New("loading model")},
		answers: []string{"", `{"difficulty":"low","priority":"normal","due":"","recur_kind":"","recur_rule":""}`},
	}
	w, st := newWorker(t, model)

	created, _ := st.Create(&store.Task{Title: "Bins"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Queue(created.ID)

	var got *store.Task
	waitFor(t, "the task to be enriched", func() bool {
		got, _ = st.Get(created.ID)
		return got != nil && got.EnrichedAt != nil
	})
	if got.Difficulty != store.DifficultyLow {
		t.Fatalf("difficulty = %q, want the retry's answer", got.Difficulty)
	}
}

func TestBacklogQueuesWhatWasMissed(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	model := &stub{}
	w, st := newWorker(t, model)

	for _, title := range []string{"One", "Two", "Three"} {
		if _, err := st.Create(&store.Task{Title: title}); err != nil {
			t.Fatalf("creating task: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Backlog()

	waitFor(t, "the backlog to clear", func() bool {
		ids, err := st.Unenriched(0)
		return err == nil && len(ids) == 0
	})
}

func TestBadAnswersAreDroppedFieldByField(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := extraction{Difficulty: "tricky", Priority: "high", Due: "next friday", RecurKind: "every", RecurRule: "weekly on funday"}

	in := e.inference(log, &store.Task{ID: 1, Title: "Send it next friday"})
	if in.Priority != store.PriorityHigh {
		t.Fatalf("priority = %q, want the one good field kept", in.Priority)
	}
	if in.Difficulty != "" || in.Due != "" || in.RecurKind != "" {
		t.Fatalf("got %+v, want the unusable fields dropped", in)
	}
}

func TestMalformedJSONIsNotWritten(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	original := backoff
	backoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { backoff = original })

	model := &stub{answers: []string{"not json", "not json", "not json"}}
	w, st := newWorker(t, model)

	created, _ := st.Create(&store.Task{Title: "Bins"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Queue(created.ID)

	waitFor(t, "the worker to give up", func() bool { return model.seen() == attempts })
	got, _ := st.Get(created.ID)
	if got.EnrichedAt != nil || got.Difficulty != "" {
		t.Fatalf("got %+v, want nothing written", got)
	}
}

func TestDoneTasksAreSkipped(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	model := &stub{}
	w, st := newWorker(t, model)

	created, _ := st.Create(&store.Task{Title: "Bins"})
	if _, err := st.Done(created.ID, 0); err != nil {
		t.Fatalf("completing: %v", err)
	}
	if err := w.enrich(context.Background(), created.ID); err != nil {
		t.Fatalf("enriching: %v", err)
	}
	if model.seen() != 0 {
		t.Fatal("a completed task should not cost a model call")
	}
}

func TestWorkerCutsTheDateOutOfTheTitle(t *testing.T) {
	fixedNow(t, "2026-09-21T09:00:00Z")
	model := &stub{answers: []string{
		`{"difficulty":"low","priority":"normal","due":"2026-09-22","recur_kind":"","recur_rule":"","title":"Email fred"}`,
	}}
	w, st := newWorker(t, model)
	created, _ := st.Create(&store.Task{Title: "Email fred by tomorrow"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Queue(created.ID)

	var got *store.Task
	waitFor(t, "the task to be enriched", func() bool {
		got, _ = st.Get(created.ID)
		return got != nil && got.EnrichedAt != nil
	})
	if got.Title != "Email fred" || got.Due != "2026-09-22" {
		t.Fatalf("got %q due %s, want the date moved out of the title", got.Title, got.Due)
	}
}
