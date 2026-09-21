// Package enrich fills in what a task did not say about itself. Adding a
// task should cost one sentence, so the daemon reads that sentence with a
// local model and writes the fields it can infer. Every surface goes through
// it: what Claude gets from the MCP tools is what typing into the CLI gets.
package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

// Model is all the worker needs from ollama, kept an interface so the tests
// can answer without a model on the machine.
type Model interface {
	Chat(ctx context.Context, system, prompt string, schema any) (json.RawMessage, error)
	Model() string
}

// queueDepth is far more than a personal list produces in a burst. Past it
// the request is dropped rather than made to wait, because a blocked queue
// would hold up the HTTP handler that fed it.
const queueDepth = 256

// attempts covers an ollama that is restarting or busy loading a model. A
// task that fails all of them stays unenriched and is picked up on the next
// start, so nothing is lost by giving up quietly here.
const attempts = 3

var backoff = []time.Duration{2 * time.Second, 15 * time.Second}

type Worker struct {
	store *store.Store
	model Model
	log   *slog.Logger
	queue chan int64
}

func New(st *store.Store, model Model, log *slog.Logger) *Worker {
	return &Worker{store: st, model: model, log: log, queue: make(chan int64, queueDepth)}
}

// Queue asks for a task to be enriched. It never blocks and never fails: a
// task that does not make it onto the queue is simply still unenriched, which
// the next start or an explicit `ordo enrich` resolves.
func (w *Worker) Queue(id int64) {
	select {
	case w.queue <- id:
	default:
		w.log.Warn("enrichment queue is full", "id", id)
	}
}

// Backlog queues every open task the worker has never reached, so a daemon
// that ran while ollama was down does not leave those rows bare for good.
func (w *Worker) Backlog() {
	ids, err := w.store.Unenriched(queueDepth)
	if err != nil {
		w.log.Error("reading the enrichment backlog", "error", err)
		return
	}
	for _, id := range ids {
		w.Queue(id)
	}
	if len(ids) > 0 {
		w.log.Info("enrichment backlog queued", "tasks", len(ids))
	}
}

// Run works the queue one task at a time until ctx is done. One at a time is
// deliberate: there is a single ollama on the box and a queue that is never
// long, so concurrency would buy contention.
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-w.queue:
			w.work(ctx, id)
		}
	}
}

func (w *Worker) work(ctx context.Context, id int64) {
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && !sleep(ctx, backoff[min(attempt-1, len(backoff)-1)]) {
			return
		}
		err := w.enrich(ctx, id)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		w.log.Warn("enrichment failed", "id", id, "attempt", attempt+1, "error", err)
	}
	w.log.Error("enrichment gave up", "id", id, "attempts", attempts)
}

func (w *Worker) enrich(ctx context.Context, id int64) error {
	t, err := w.store.Get(id)
	if err != nil {
		return err
	}
	if t.Status != store.StatusOpen {
		return nil
	}
	raw, err := w.model.Chat(ctx, systemPrompt, taskPrompt(t), Schema)
	if err != nil {
		return err
	}
	var e extraction
	if err := json.Unmarshal(raw, &e); err != nil {
		return fmt.Errorf("task %d: model answered %s: %w", id, raw, err)
	}
	in := e.inference(w.log, id)
	after, err := w.store.Enrich(id, in)
	if err != nil {
		return err
	}
	w.log.Info("enriched", "id", id, "difficulty", after.Difficulty, "priority", after.Priority,
		"due", after.Due, "recur", after.RecurRule)
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
