// Package publish keeps a calendar equal to the plan. It never remembers
// what it wrote: each pass recomputes the day, lists what the calendar has,
// and makes the second match the first. That is the only way a plan that is
// itself recomputed on every read can be shown somewhere else without the
// two drifting apart.
package publish

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/gcal"
	"github.com/cameronpyne-smith/ordo/internal/schedule"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// Calendar is what the publisher needs of a calendar, which is little enough
// that a test can be one in memory.
type Calendar interface {
	List(ctx context.Context, from, to time.Time) ([]gcal.Event, error)
	Insert(ctx context.Context, e gcal.Event) (gcal.Event, error)
	Update(ctx context.Context, e gcal.Event) (gcal.Event, error)
	Delete(ctx context.Context, id string) error
}

// Planner is the daemon's own answer to what a day looks like. The publisher
// adds nothing to it; it is a way of seeing the same plan elsewhere.
type Planner interface {
	Plan(ctx context.Context, day string) (schedule.Day, error)
}

const (
	// every is the safety net. A plan for today moves with the clock even
	// when nothing about the list changes, so the calendar is brought back
	// into line on a schedule as well as on demand.
	every = 5 * time.Minute

	// settle is how long a nudge waits for the next one. Finishing a task
	// and the model reading the one added after it arrive seconds apart,
	// and one pass covers both.
	settle = 2 * time.Second

	syncTimeout = 45 * time.Second
)

type Publisher struct {
	cal    Calendar
	days   int
	log    *slog.Logger
	nudges chan struct{}
}

// New prepares a publisher for the next days days, today included.
func New(cal Calendar, days int, log *slog.Logger) *Publisher {
	if days < 1 {
		days = 1
	}
	return &Publisher{cal: cal, days: days, log: log, nudges: make(chan struct{}, 1)}
}

// Nudge says the plan may have changed. It never blocks and is safe on a nil
// publisher, so the service can call it without knowing whether publishing
// is configured, the way it queues enrichment.
func (p *Publisher) Nudge() {
	if p == nil {
		return
	}
	select {
	case p.nudges <- struct{}{}:
	default:
	}
}

// Run publishes once, then on every nudge and on the clock, until the
// context ends. A nil publisher returns at once.
func (p *Publisher) Run(ctx context.Context, plan Planner) {
	if p == nil {
		return
	}
	p.sync(ctx, plan)
	tick := time.NewTicker(every)
	defer tick.Stop()
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.sync(ctx, plan)
		case <-p.nudges:
			pending = time.After(settle)
		case <-pending:
			pending = nil
			p.sync(ctx, plan)
		}
	}
}

func (p *Publisher) sync(ctx context.Context, plan Planner) {
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	result, err := p.Sync(ctx, plan)
	if err != nil {
		p.log.Warn("publishing failed", "error", err)
		return
	}
	if result.changed() {
		p.log.Info("published", "created", result.Created, "updated", result.Updated,
			"removed", result.Removed, "kept", result.Kept)
	}
}

// Result is what one pass did, for the log.
type Result struct {
	Created, Updated, Removed, Kept int
}

func (r Result) changed() bool { return r.Created+r.Updated+r.Removed > 0 }

func (r Result) add(o Result) Result {
	return Result{r.Created + o.Created, r.Updated + o.Updated, r.Removed + o.Removed, r.Kept + o.Kept}
}

// Sync makes the calendar match the plan for each day in the horizon. A day
// that fails stops the pass; the next one starts from the calendar as it is,
// so nothing has to be remembered across the failure.
func (p *Publisher) Sync(ctx context.Context, plan Planner) (Result, error) {
	var total Result
	first, err := store.ParseDate(store.Today())
	if err != nil {
		return total, err
	}
	for i := 0; i < p.days; i++ {
		date := first.AddDate(0, 0, i)
		day, err := plan.Plan(ctx, date.Format(store.DateFormat))
		if err != nil {
			return total, fmt.Errorf("planning %s: %w", date.Format(store.DateFormat), err)
		}
		existing, err := p.cal.List(ctx, date, date.AddDate(0, 0, 1))
		if err != nil {
			return total, err
		}
		result, err := reconcile(ctx, p.cal, desired(day), existing)
		total = total.add(result)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// desired is the plan as events: one per block, the title as the summary
// and the reason as the description, so a block on a phone still says why
// it is there.
func desired(day schedule.Day) []gcal.Event {
	out := make([]gcal.Event, 0, len(day.Blocks))
	for _, b := range day.Blocks {
		summary := b.Task.Title
		if b.Task.PinnedFor(day.Date) {
			summary = "📌 " + summary
		}
		out = append(out, gcal.Event{
			Task:        b.Task.ID,
			Summary:     summary,
			Description: b.Reason,
			Start:       b.Start,
			End:         b.End,
		})
	}
	return out
}

// reconcile matches events to blocks by task. A block with no event is
// inserted, an event with no block is removed, a pair that differ is
// patched, and an event with no task was made by a person and is left
// exactly as it is.
func reconcile(ctx context.Context, cal Calendar, want, existing []gcal.Event) (Result, error) {
	var r Result
	have := map[int64]gcal.Event{}
	var extra []gcal.Event
	for _, e := range existing {
		if e.Task == 0 {
			continue
		}
		if _, dup := have[e.Task]; dup {
			extra = append(extra, e)
			continue
		}
		have[e.Task] = e
	}
	for _, w := range want {
		e, ok := have[w.Task]
		if !ok {
			if _, err := cal.Insert(ctx, w); err != nil {
				return r, err
			}
			r.Created++
			continue
		}
		delete(have, w.Task)
		if same(e, w) {
			r.Kept++
			continue
		}
		w.ID = e.ID
		if _, err := cal.Update(ctx, w); err != nil {
			return r, err
		}
		r.Updated++
	}
	for _, e := range have {
		extra = append(extra, e)
	}
	for _, e := range extra {
		if err := cal.Delete(ctx, e.ID); err != nil {
			return r, err
		}
		r.Removed++
	}
	return r, nil
}

func same(a, b gcal.Event) bool {
	return a.Summary == b.Summary && a.Description == b.Description &&
		a.Start.Equal(b.Start) && a.End.Equal(b.End)
}
