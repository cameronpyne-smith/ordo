package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// feed serves an ICS body the way a published calendar does, so the tests
// exercise the fetch as well as the parse.
func feed(t *testing.T, events ...string) *Client {
	t.Helper()
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//ordo//test//EN\r\n" +
		strings.Join(events, "") + "END:VCALENDAR\r\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, nil)
}

func event(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func busyOn(t *testing.T, c *Client, date string) []Busy {
	t.Helper()
	from := day(date)
	got, err := c.Busy(context.Background(), from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("reading the calendar: %v", err)
	}
	return got
}

func show(busy []Busy) []string {
	var out []string
	for _, b := range busy {
		out = append(out, b.Start.UTC().Format("15:04")+"-"+b.End.UTC().Format("15:04"))
	}
	return out
}

// No feed configured is a normal state, not an error: the day is then only
// its preferences.
func TestNoFeedIsNotAnError(t *testing.T) {
	var c *Client
	if New("", nil) != nil {
		t.Fatal("an empty URL should give no client")
	}
	if c.Configured() {
		t.Fatal("a nil client must not claim to be configured")
	}
	busy, err := c.Busy(context.Background(), day("2026-09-21"), day("2026-09-22"))
	if err != nil || busy != nil {
		t.Fatalf("busy = %v, err = %v; want nothing and no error", busy, err)
	}
}

func TestOneEvent(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Dentist",
		"DTSTART:20260921T090000Z", "DTEND:20260921T094500Z"))

	got := busyOn(t, c, "2026-09-21")
	if len(got) != 1 || got[0].Summary != "Dentist" {
		t.Fatalf("busy = %+v", got)
	}
	if show(got)[0] != "09:00-09:45" {
		t.Errorf("span = %s", show(got)[0])
	}
}

func TestEventOutsideTheWindowIsIgnored(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Last week",
		"DTSTART:20260914T090000Z", "DTEND:20260914T100000Z"))
	if got := busyOn(t, c, "2026-09-21"); len(got) != 0 {
		t.Fatalf("busy = %+v, want nothing", got)
	}
}

func TestDurationInsteadOfAnEnd(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Standup",
		"DTSTART:20260921T093000Z", "DURATION:PT1H30M"))
	if got := show(busyOn(t, c, "2026-09-21")); len(got) != 1 || got[0] != "09:30-11:00" {
		t.Fatalf("spans = %v, want the duration applied", got)
	}
}

// An all-day event takes the whole day, which is how a holiday or a day off
// has to read to a scheduler.
func TestAllDayEventTakesTheDay(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Annual leave",
		"DTSTART;VALUE=DATE:20260921", "DTEND;VALUE=DATE:20260922"))
	got := busyOn(t, c, "2026-09-21")
	if len(got) != 1 {
		t.Fatalf("busy = %+v, want the whole day taken", got)
	}
	if hours := got[0].End.Sub(got[0].Start).Hours(); hours != 24 {
		t.Errorf("all-day event spans %v hours, want 24", hours)
	}
}

// Most of what fills a calendar repeats, so a reader that ignored RRULE
// would miss the majority of a real week.
func TestWeeklyRecurrence(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Climbing",
		"DTSTART:20260902T180000Z", "DTEND:20260902T200000Z",
		"RRULE:FREQ=WEEKLY;BYDAY=WE"))

	// 2026-09-23 is a Wednesday three weeks after the first occurrence.
	if got := show(busyOn(t, c, "2026-09-23")); len(got) != 1 || got[0] != "18:00-20:00" {
		t.Fatalf("spans = %v, want the weekly occurrence", got)
	}
	// 2026-09-24 is the Thursday after it.
	if got := busyOn(t, c, "2026-09-24"); len(got) != 0 {
		t.Fatalf("busy = %+v, want nothing on a day the rule misses", got)
	}
}

func TestRecurrenceRespectsAnExclusion(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Climbing",
		"DTSTART:20260902T180000Z", "DTEND:20260902T200000Z",
		"RRULE:FREQ=WEEKLY;BYDAY=WE",
		"EXDATE:20260923T180000Z"))
	if got := busyOn(t, c, "2026-09-23"); len(got) != 0 {
		t.Fatalf("busy = %+v, want the cancelled week skipped", got)
	}
	if got := busyOn(t, c, "2026-09-30"); len(got) != 1 {
		t.Fatalf("busy = %+v, want the following week still there", got)
	}
}

func TestRecurrenceStopsAtUntil(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Course",
		"DTSTART:20260902T180000Z", "DTEND:20260902T193000Z",
		"RRULE:FREQ=WEEKLY;BYDAY=WE;UNTIL=20260916T180000Z"))
	if got := busyOn(t, c, "2026-09-16"); len(got) != 1 {
		t.Fatalf("busy = %+v, want the last occurrence", got)
	}
	if got := busyOn(t, c, "2026-09-23"); len(got) != 0 {
		t.Fatalf("busy = %+v, want nothing after UNTIL", got)
	}
}

// Time marked free leaves the time available; so does a cancelled meeting.
// Treating either as busy would quietly shrink the day.
func TestTransparentAndCancelledEventsTakeNothing(t *testing.T) {
	c := feed(t,
		event("UID:1", "SUMMARY:Out of office", "DTSTART:20260921T090000Z",
			"DTEND:20260921T170000Z", "TRANSP:TRANSPARENT"),
		event("UID:2", "SUMMARY:Cancelled call", "DTSTART:20260921T110000Z",
			"DTEND:20260921T113000Z", "STATUS:CANCELLED"))
	if got := busyOn(t, c, "2026-09-21"); len(got) != 0 {
		t.Fatalf("busy = %+v, want nothing taken", got)
	}
}

// An event that runs into the window from the day before still takes the
// time it overlaps.
func TestEventSpanningIntoTheWindow(t *testing.T) {
	c := feed(t, event(
		"UID:1", "SUMMARY:Night shift",
		"DTSTART:20260920T220000Z", "DTEND:20260921T060000Z"))
	if got := busyOn(t, c, "2026-09-21"); len(got) != 1 {
		t.Fatalf("busy = %+v, want the overlap counted", got)
	}
}

// One unreadable event must not cost the rest of the calendar.
func TestABadEventDoesNotLoseTheGoodOnes(t *testing.T) {
	c := feed(t,
		event("UID:1", "SUMMARY:Broken", "DTSTART:20260921T090000Z",
			"DTEND:20260921T100000Z", "RRULE:FREQ=NONSENSE"),
		event("UID:2", "SUMMARY:Fine", "DTSTART:20260921T140000Z", "DTEND:20260921T150000Z"))
	got := busyOn(t, c, "2026-09-21")
	if len(got) != 1 || got[0].Summary != "Fine" {
		t.Fatalf("busy = %+v, want the readable event kept", got)
	}
}

func TestUnreachableFeedIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(srv.URL, nil)
	if _, err := c.Busy(context.Background(), day("2026-09-21"), day("2026-09-22")); err == nil {
		t.Fatal("want an error when the feed refuses")
	}
}

func TestMergeCollapsesOverlaps(t *testing.T) {
	base := day("2026-09-21")
	at := func(h, m int) time.Time { return base.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	got := Merge([]Busy{
		{Start: at(14, 0), End: at(15, 0), Summary: "second"},
		{Start: at(9, 0), End: at(10, 0), Summary: "first"},
		{Start: at(9, 30), End: at(11, 0), Summary: "overlapping"},
		{Start: at(11, 0), End: at(11, 30), Summary: "touching"},
	})
	if len(got) != 2 {
		t.Fatalf("merged = %+v, want two runs", got)
	}
	if show(got)[0] != "09:00-11:30" || show(got)[1] != "14:00-15:00" {
		t.Fatalf("spans = %v", show(got))
	}
	if !strings.Contains(got[0].Summary, "first") || !strings.Contains(got[0].Summary, "touching") {
		t.Errorf("summary = %q, want it to name what was merged", got[0].Summary)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"PT1H", time.Hour},
		{"PT30M", 30 * time.Minute},
		{"PT1H30M", 90 * time.Minute},
		{"P1D", 24 * time.Hour},
		{"P1W", 7 * 24 * time.Hour},
		{"PT45S", 45 * time.Second},
		{"-PT1H", -time.Hour},
	}
	for _, c := range cases {
		got, err := parseDuration(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "1H", "PT", "PTXH", "P1Y"} {
		if _, err := parseDuration(bad); err == nil {
			t.Errorf("%q parsed, want an error", bad)
		}
	}
}

// A feed read once and then lost is answered from the copy, with a *Stale
// error that says so; the next good read replaces the copy.
func TestALostFeedIsAnsweredFromTheLastCopy(t *testing.T) {
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//ordo//test//EN\r\n" +
		event("UID:1", "SUMMARY:Work", "DTSTART:20260921T090000Z", "DTEND:20260921T170000Z") +
		"END:VCALENDAR\r\n"
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	c := New(srv.URL, nil)
	from, to := day("2026-09-21"), day("2026-09-22")

	if _, err := c.Busy(context.Background(), from, to); err != nil {
		t.Fatal(err)
	}
	down.Store(true)
	got, err := c.Busy(context.Background(), from, to)
	var stale *Stale
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *Stale", err)
	}
	if !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "using the copy read") {
		t.Errorf("err = %q, want the failure and the copy both named", err)
	}
	if len(got) != 1 || got[0].Summary != "Work" {
		t.Fatalf("busy = %+v, want work from the copy", got)
	}

	down.Store(false)
	if _, err := c.Busy(context.Background(), from, to); err != nil {
		t.Fatalf("a feed that came back is still reported stale: %v", err)
	}
}
