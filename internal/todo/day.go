package todo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/calendar"
	"github.com/cameronpyne-smith/ordo/internal/schedule"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// calendarTimeout bounds what a day view waits for an external feed. Past it
// the plan is built from the last copy of the feed, because a slow calendar
// must not turn into a slow answer to "what am I doing today".
const calendarTimeout = 10 * time.Second

// Today plans a day. A feed that cannot be read is reported alongside the
// plan rather than failing the request: asked directly, a plan with a
// warning on it is still an answer.
func (s *Service) Today(ctx context.Context, day string) (api.TodayResponse, error) {
	plan, calErr, err := s.plan(ctx, day)
	if err != nil {
		return api.TodayResponse{}, err
	}
	message := ""
	if calErr != nil {
		message = calErr.Error()
	}
	logged, err := s.store.LoggedOn(plan.Date)
	if err != nil {
		return api.TodayResponse{}, err
	}
	out := api.FromDay(plan, s.calendar.Configured(), message)
	out.Done, out.Tally = api.FromLogged(logged)
	return out, nil
}

// Plan is Today before it is shaped for the wire, for the publisher, which
// wants the times as times. A plan from an old copy of the feed is still the
// best answer there is. A plan made with no copy at all is refused: nothing
// asked for it, and publishing it would fill the working day with tasks on
// every phone the calendar reaches, where leaving the last published plan
// alone costs nothing until the feed comes back.
func (s *Service) Plan(ctx context.Context, day string) (schedule.Day, error) {
	plan, calErr, err := s.plan(ctx, day)
	if err != nil {
		return plan, err
	}
	var stale *calendar.Stale
	if calErr != nil && !errors.As(calErr, &stale) {
		return schedule.Day{}, fmt.Errorf("not planning %s without the calendar: %w", day, calErr)
	}
	return plan, nil
}

// plan returns what the calendar said about itself apart from the plan, so
// each caller decides what an unreadable feed means for it.
func (s *Service) plan(ctx context.Context, day string) (schedule.Day, error, error) {
	if day == "" {
		day = store.Today()
	}
	if _, err := store.ParseDate(day); err != nil {
		return schedule.Day{}, nil, fmt.Errorf("today: day %q must be YYYY-MM-DD: %w", day, store.ErrInvalid)
	}
	prefs, err := s.store.Preferences()
	if err != nil {
		return schedule.Day{}, nil, err
	}
	tasks, err := s.store.List(store.Filter{Status: store.StatusOpen})
	if err != nil {
		return schedule.Day{}, nil, err
	}

	busy, calErr := s.busy(ctx, day)
	plan, err := schedule.Plan(schedule.Options{Day: day, Tasks: tasks, Busy: busy, Prefs: prefs})
	if err != nil {
		return schedule.Day{}, nil, err
	}
	var stale *calendar.Stale
	switch {
	case errors.As(calErr, &stale):
		s.log.Warn("planning from an old copy of the calendar", "day", day, "error", calErr)
	case calErr != nil:
		s.log.Warn("planning without the calendar", "day", day, "error", calErr)
	}
	return plan, calErr, nil
}

func (s *Service) busy(ctx context.Context, day string) ([]calendar.Busy, error) {
	if !s.calendar.Configured() {
		return nil, nil
	}
	date, err := store.ParseDate(day)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, calendarTimeout)
	defer cancel()
	return s.calendar.Busy(ctx, date, date.AddDate(0, 0, 1))
}

// Pin marks a task as wanted on a day, and Unpin takes that back. This is the
// one input the daemon's own ordering cannot derive, so it is a verb of its
// own rather than a field on an edit.
func (s *Service) Pin(id int64, day string) (api.Task, error) {
	t, err := s.store.Pin(id, day)
	if err != nil {
		return api.Task{}, err
	}
	s.changed()
	return api.FromTask(t), nil
}

func (s *Service) Unpin(id int64) (api.Task, error) {
	t, err := s.store.Unpin(id)
	if err != nil {
		return api.Task{}, err
	}
	s.changed()
	return api.FromTask(t), nil
}

func (s *Service) Preferences() (api.Preferences, error) {
	p, err := s.store.Preferences()
	if err != nil {
		return api.Preferences{}, err
	}
	return api.FromPreferences(p), nil
}

// SetPreferences reads, applies and validates as one step, so a request that
// would leave the day incoherent is refused whole rather than half-written.
func (s *Service) SetPreferences(req api.PreferencesRequest) (api.Preferences, error) {
	if req.Empty() {
		return api.Preferences{}, fmt.Errorf("preferences: nothing to change: %w", store.ErrInvalid)
	}
	current, err := s.store.Preferences()
	if err != nil {
		return api.Preferences{}, err
	}
	next, err := req.Apply(current)
	if err != nil {
		return api.Preferences{}, err
	}
	saved, err := s.store.SetPreferences(next)
	if err != nil {
		return api.Preferences{}, err
	}
	s.changed()
	return api.FromPreferences(saved), nil
}
