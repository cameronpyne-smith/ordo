package todo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/calendar"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// 2030-01-07 is a Monday far enough ahead that the plan is never clipped to
// the clock, and in winter, so London is UTC and the feed's times read as
// written.
const monday = "2030-01-07"

const workFeed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//ordo//test//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:work\r\nSUMMARY:Work\r\n" +
	"DTSTART:20300107T090000Z\r\nDTEND:20300107T170000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// withFeed is a service whose calendar can be switched off, the way a
// published feed goes away for a while and comes back.
func withFeed(t *testing.T) (*Service, *atomic.Bool) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Create(&store.Task{Title: "Wardrobe into office", EstimateMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	down := &atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, workFeed)
	}))
	t.Cleanup(srv.Close)
	return New(Options{Store: st, Calendar: calendar.New(srv.URL, nil)}), down
}

// With no copy of the feed there is nothing to say work is there. Asked
// directly, the plan is still an answer, with the warning on it; the
// publisher is refused, so a glitch never reaches the phone.
func TestAFeedNeverReadIsNotPublished(t *testing.T) {
	svc, down := withFeed(t)
	down.Store(true)

	resp, err := svc.Today(context.Background(), monday)
	if err != nil {
		t.Fatal(err)
	}
	if resp.CalendarError == "" {
		t.Error("a plan made without the calendar did not say so")
	}
	if _, err := svc.Plan(context.Background(), monday); err == nil {
		t.Fatal("the publisher was handed a plan made without the calendar")
	}
}

// Once the feed has been read, losing it plans from the copy: work stays
// busy, the publisher carries on, and the answer says which copy it used.
func TestAFeedThatGoesAwayIsPlannedFromTheLastCopy(t *testing.T) {
	svc, down := withFeed(t)
	if _, err := svc.Plan(context.Background(), monday); err != nil {
		t.Fatal(err)
	}
	down.Store(true)

	plan, err := svc.Plan(context.Background(), monday)
	if err != nil {
		t.Fatalf("the publisher was refused a plan from the last copy: %v", err)
	}
	if len(plan.Busy) != 1 || plan.Busy[0].Summary != "Work" {
		t.Fatalf("busy = %+v, want work from the copy", plan.Busy)
	}
	for _, b := range plan.Blocks {
		if b.Start.Hour() >= 9 && b.Start.Hour() < 17 {
			t.Errorf("%q planned at %s, inside work", b.Task.Title, b.Start.Format("15:04"))
		}
	}
	resp, err := svc.Today(context.Background(), monday)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.CalendarError, "using the copy read") {
		t.Errorf("calendar error = %q, want it to say the copy was used", resp.CalendarError)
	}
}
