// Package calendar turns a published ICS feed into the times a day is
// already spoken for, work included. It reads and nothing else; the plan is
// published to a different calendar, by internal/publish.
//
// A feed is optional. With none configured the client is nil and reports no
// busy time. Once one is configured it is what keeps tasks out of the
// working day, so an unreadable feed falls back to the last copy that could
// be read rather than to an empty diary.
package calendar

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

// Busy is one stretch of time the calendar says is taken. The summary is
// carried so a day view can say what took it, and nothing else about the
// event is read.
type Busy struct {
	Start   time.Time
	End     time.Time
	Summary string
}

type Client struct {
	url  string
	http *http.Client
	log  *slog.Logger

	mu   sync.Mutex
	last *ics.Calendar
	read time.Time
}

// Stale is what Busy returns, alongside an answer, when the feed could not
// be read and the answer comes from the last copy that could. The copy is
// the better guess by far: most of a calendar repeats, so an hour-old copy
// still has the working week in it, where no copy would plan tasks into the
// middle of it.
type Stale struct {
	Read time.Time
	Err  error
}

func (s *Stale) Error() string {
	return fmt.Sprintf("%v; using the copy read %s ago", s.Err, ago(time.Since(s.Read)))
}

func (s *Stale) Unwrap() error { return s.Err }

func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// New returns nil when no URL is configured, which every method tolerates.
// The caller then has one less branch and the "no calendar" path is the same
// code as the "calendar is empty" path.
func New(url string, log *slog.Logger) *Client {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Client{
		url:  url,
		http: &http.Client{Timeout: 15 * time.Second},
		log:  log,
	}
}

func (c *Client) Configured() bool { return c != nil }

// Busy returns the taken stretches overlapping [from, to), merged and in
// order. A single unreadable event is skipped rather than failing the fetch:
// most of a calendar is better than none of it. A feed that cannot be read
// at all is answered from the last copy that could, with a *Stale error
// saying so; only when there has never been a copy is the answer nothing.
func (c *Client) Busy(ctx context.Context, from, to time.Time) ([]Busy, error) {
	if c == nil {
		return nil, nil
	}
	cal, err := c.fetch(ctx)
	c.mu.Lock()
	if err == nil {
		c.last, c.read = cal, time.Now()
	} else if c.last != nil {
		cal, err = c.last, &Stale{Read: c.read, Err: err}
	}
	c.mu.Unlock()
	if cal == nil {
		return nil, err
	}
	return c.expandAll(cal, from, to), err
}

func (c *Client) expandAll(cal *ics.Calendar, from, to time.Time) []Busy {
	var out []Busy
	for _, event := range cal.Events() {
		spans, err := expand(event, from, to)
		if err != nil {
			c.log.Warn("skipping a calendar event", "error", err)
			continue
		}
		out = append(out, spans...)
	}
	return Merge(out)
}

func (c *Client) fetch(ctx context.Context) (*ics.Calendar, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("reading the calendar: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading the calendar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading the calendar: %s", resp.Status)
	}
	cal, err := ics.ParseCalendar(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing the calendar: %w", err)
	}
	return cal, nil
}

// expand turns one VEVENT into the occurrences that fall in the window,
// walking its RRULE when it has one.
func expand(event *ics.VEvent, from, to time.Time) ([]Busy, error) {
	if free(event) {
		return nil, nil
	}
	start, end, err := span(event)
	if err != nil {
		return nil, err
	}
	summary := ""
	if p := event.GetProperty(ics.ComponentPropertySummary); p != nil {
		summary = p.Value
	}
	length := end.Sub(start)
	if length <= 0 {
		return nil, nil
	}

	rule := event.GetProperty(ics.ComponentPropertyRrule)
	if rule == nil {
		if start.Before(to) && end.After(from) {
			return []Busy{{Start: start, End: end, Summary: summary}}, nil
		}
		return nil, nil
	}

	opts, err := rrule.StrToROption(rule.Value)
	if err != nil {
		return nil, fmt.Errorf("rrule %q: %w", rule.Value, err)
	}
	opts.Dtstart = start
	set, err := rrule.NewRRule(*opts)
	if err != nil {
		return nil, fmt.Errorf("rrule %q: %w", rule.Value, err)
	}
	skip := excluded(event)

	var out []Busy
	// An occurrence starting before the window can still run into it, so the
	// search starts one event-length early.
	for _, at := range set.Between(from.Add(-length), to, true) {
		if skip[at.UTC()] {
			continue
		}
		finish := at.Add(length)
		if !at.Before(to) || !finish.After(from) {
			continue
		}
		out = append(out, Busy{Start: at, End: finish, Summary: summary})
	}
	return out, nil
}

// span reads an event's start and end, covering the three ways a calendar
// says how long something is: an explicit end, a duration, or a whole day.
func span(event *ics.VEvent) (time.Time, time.Time, error) {
	start, err := event.GetStartAt()
	if err != nil {
		allDay, dayErr := event.GetAllDayStartAt()
		if dayErr != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("event has no usable start: %w", err)
		}
		return allDay, allDay.AddDate(0, 0, 1), nil
	}
	if end, err := event.GetEndAt(); err == nil {
		return start, end, nil
	}
	if p := event.GetProperty(ics.ComponentPropertyDuration); p != nil {
		d, err := parseDuration(p.Value)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return start, start.Add(d), nil
	}
	// RFC 5545: an event with neither end nor duration takes no time at all.
	return start, start, nil
}

// free reports whether an event leaves the time available: one marked
// transparent, or cancelled, blocks nothing.
func free(event *ics.VEvent) bool {
	if p := event.GetProperty(ics.ComponentPropertyTransp); p != nil &&
		strings.EqualFold(p.Value, "TRANSPARENT") {
		return true
	}
	if p := event.GetProperty(ics.ComponentPropertyStatus); p != nil &&
		strings.EqualFold(p.Value, "CANCELLED") {
		return true
	}
	return false
}

func excluded(event *ics.VEvent) map[time.Time]bool {
	skip := map[time.Time]bool{}
	for _, p := range event.Properties {
		if !strings.EqualFold(p.IANAToken, string(ics.ComponentPropertyExdate)) {
			continue
		}
		for _, value := range strings.Split(p.Value, ",") {
			if when, err := parseICSTime(strings.TrimSpace(value)); err == nil {
				skip[when.UTC()] = true
			}
		}
	}
	return skip
}

func parseICSTime(s string) (time.Time, error) {
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("time %q is not an ICS timestamp", s)
}

// parseDuration reads the ISO 8601 durations a calendar actually emits.
func parseDuration(s string) (time.Duration, error) {
	original := s
	sign := time.Duration(1)
	s = strings.ToUpper(strings.TrimSpace(s))
	if strings.HasPrefix(s, "-") {
		sign, s = -1, s[1:]
	}
	s = strings.TrimPrefix(s, "+")
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("duration %q is not ISO 8601", original)
	}
	s = s[1:]

	var total time.Duration
	var number strings.Builder
	inTime, components := false, 0
	for _, r := range s {
		switch {
		case r == 'T':
			inTime = true
		case r >= '0' && r <= '9':
			number.WriteRune(r)
		default:
			n, err := parseInt(number.String())
			if err != nil {
				return 0, fmt.Errorf("duration %q is not ISO 8601", original)
			}
			number.Reset()
			unit, ok := durationUnit(r, inTime)
			if !ok {
				return 0, fmt.Errorf("duration %q has an unsupported unit %q", original, string(r))
			}
			total += time.Duration(n) * unit
			components++
		}
	}
	// A trailing number with no unit, or no components at all, is not a
	// duration however forgiving you feel.
	if number.Len() > 0 || components == 0 {
		return 0, fmt.Errorf("duration %q is not ISO 8601", original)
	}
	return sign * total, nil
}

func durationUnit(r rune, inTime bool) (time.Duration, bool) {
	switch r {
	case 'W':
		return 7 * 24 * time.Hour, true
	case 'D':
		return 24 * time.Hour, true
	case 'H':
		return time.Hour, true
	case 'M':
		if inTime {
			return time.Minute, true
		}
		return 0, false
	case 'S':
		return time.Second, true
	}
	return 0, false
}

func parseInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// Merge collapses overlapping and touching stretches, so the packer sees one
// unavailable run rather than three back-to-back meetings.
func Merge(in []Busy) []Busy {
	if len(in) == 0 {
		return nil
	}
	sorted := make([]Busy, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start.Equal(sorted[j].Start) {
			return sorted[i].End.Before(sorted[j].End)
		}
		return sorted[i].Start.Before(sorted[j].Start)
	})

	out := []Busy{sorted[0]}
	for _, b := range sorted[1:] {
		last := &out[len(out)-1]
		if b.Start.After(last.End) {
			out = append(out, b)
			continue
		}
		if b.End.After(last.End) {
			last.End = b.End
		}
		if last.Summary != b.Summary && b.Summary != "" {
			last.Summary = last.Summary + ", " + b.Summary
		}
	}
	return out
}
