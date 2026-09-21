// Package recur parses the recurrence grammar and computes when a recurring
// task next falls due. The grammar is small and fixed on purpose: it has to
// be readable in a list, learnable by a small extraction model, and
// explainable from the rule text alone.
//
//	every: daily | weekly on tue | weekly on mon,thu | monthly on 1 |
//	       monthly on last | yearly on 03-15 | 3d | 2w | 1m
//	after: 3d | 2w | 1m
//
// Both kinds take the interval form, and it is the kind that separates them:
// every 2w is a fortnightly cycle counted from the occurrence, after 2w is
// two weeks from the day the task was last actually done.
//
// Dates are plain calendar dates, YYYY-MM-DD. Everything here is arithmetic
// on those, so it runs in UTC and never meets a DST boundary.
package recur

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	KindEvery = "every"
	KindAfter = "after"
)

const DateFormat = "2006-01-02"

// Normalise validates a rule and returns its canonical spelling, so that
// "Weekly on THU, mon" and "weekly on mon,thu" are the same stored string.
func Normalise(kind, rule string) (string, error) {
	s, err := parse(kind, rule)
	if err != nil {
		return "", err
	}
	return s.rule, nil
}

// Interval reports whether a rule is the count-and-unit form, which display
// has to know: "weekly on tue" says what it means on its own and "2w" does
// not.
func Interval(kind, rule string) bool {
	s, err := parse(kind, rule)
	return err == nil && s.count > 0
}

// First is the occurrence a newly recurring task starts on: for every, the
// first occurrence falling on or after on; for after, on itself, since an
// interval task is on the list from the moment it is added and only moves
// once it has been done.
func First(kind, rule, on string) (string, error) {
	return occurrence(kind, rule, on, true)
}

// Next is where a task lands after being completed on the given date. For
// every this is the next occurrence strictly after it, so missed occurrences
// never accumulate; for after it is that date plus the interval.
func Next(kind, rule, on string) (string, error) {
	return occurrence(kind, rule, on, false)
}

func occurrence(kind, rule, on string, inclusive bool) (string, error) {
	s, err := parse(kind, rule)
	if err != nil {
		return "", err
	}
	day, err := parseDate(on)
	if err != nil {
		return "", err
	}
	// An interval is the same arithmetic whichever kind it belongs to. What
	// differs is the date the caller hands in: the occurrence for every, the
	// day it was done for after.
	if s.count > 0 {
		if inclusive {
			return on, nil
		}
		return format(s.addInterval(day)), nil
	}
	found, ok := s.after(day, inclusive)
	if !ok {
		return "", fmt.Errorf("rule %q has no occurrence after %s", s.rule, on)
	}
	return format(found), nil
}

type schedule struct {
	kind string
	rule string // canonical spelling

	daily     bool
	weekdays  []time.Weekday
	monthDay  int // 1-31
	monthLast bool
	yearMonth time.Month
	yearDay   int

	count int
	unit  string // d | w | m
}

func parse(kind, rule string) (schedule, error) {
	switch kind {
	case KindEvery:
		return parseEvery(rule)
	case KindAfter:
		return parseAfter(rule)
	case "":
		return schedule{}, fmt.Errorf("recurrence kind is required")
	}
	return schedule{}, fmt.Errorf("recurrence kind %q must be every or after", kind)
}

func parseEvery(rule string) (schedule, error) {
	tokens := strings.Fields(strings.ToLower(rule))
	if len(tokens) == 0 {
		return schedule{}, fmt.Errorf("every needs a rule, such as %q", "weekly on tue")
	}
	s := schedule{kind: KindEvery}
	period := tokens[0]
	if period == "daily" {
		if len(tokens) > 1 {
			return s, fmt.Errorf("every %q: daily takes no arguments", rule)
		}
		s.daily, s.rule = true, "daily"
		return s, nil
	}
	// One token that is not a period word is the interval form, which is how
	// a fortnightly cycle is said: there is no anchored rule for it.
	if len(tokens) == 1 && !isPeriod(period) {
		return parseInterval(KindEvery, period)
	}
	if len(tokens) < 3 || tokens[1] != "on" {
		return s, fmt.Errorf("every %q: expected %q, %q, %q, %q, %q or an interval such as %q",
			rule, "daily", "weekly on tue", "monthly on 1", "monthly on last", "yearly on 03-15", "2w")
	}
	// Joining without a separator lets "mon, thu" and "mon,thu" both work.
	arg := strings.Join(tokens[2:], "")

	switch period {
	case "weekly":
		days, err := parseWeekdays(arg)
		if err != nil {
			return s, err
		}
		s.weekdays = days
		s.rule = "weekly on " + formatWeekdays(days)
	case "monthly":
		if arg == "last" {
			s.monthLast, s.rule = true, "monthly on last"
			break
		}
		day, err := strconv.Atoi(arg)
		if err != nil || day < 1 || day > 31 {
			return s, fmt.Errorf("monthly on %q: expected a day from 1 to 31, or last", arg)
		}
		s.monthDay, s.rule = day, "monthly on "+strconv.Itoa(day)
	case "yearly":
		when, err := time.Parse("01-02", arg)
		if err != nil {
			return s, fmt.Errorf("yearly on %q: expected MM-DD, such as 03-15", arg)
		}
		s.yearMonth, s.yearDay = when.Month(), when.Day()
		s.rule = "yearly on " + arg
	default:
		return s, fmt.Errorf("every %q: period must be daily, weekly, monthly or yearly", period)
	}
	return s, nil
}

func parseAfter(rule string) (schedule, error) {
	text := strings.ToLower(strings.Join(strings.Fields(rule), ""))
	if text == "" {
		return schedule{kind: KindAfter}, fmt.Errorf("after needs an interval, such as %q", "3d")
	}
	return parseInterval(KindAfter, text)
}

// parseInterval reads the count-and-unit form that both kinds share.
func parseInterval(kind, text string) (schedule, error) {
	s := schedule{kind: kind}
	unit := text[len(text)-1:]
	switch unit {
	case "d", "w", "m":
	default:
		return s, fmt.Errorf("%s %q: unit must be d, w or m, as in %q", kind, text, "3d")
	}
	count, err := strconv.Atoi(text[:len(text)-1])
	if err != nil || count < 1 {
		return s, fmt.Errorf("%s %q: expected a count then a unit, as in %q", kind, text, "3d")
	}
	s.count, s.unit = count, unit
	s.rule = strconv.Itoa(count) + unit
	return s, nil
}

func isPeriod(token string) bool {
	switch token {
	case "weekly", "monthly", "yearly":
		return true
	}
	return false
}

func parseWeekdays(arg string) ([]time.Weekday, error) {
	names := map[string]time.Weekday{
		"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
		"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
	}
	seen := map[time.Weekday]bool{}
	var days []time.Weekday
	for _, name := range strings.Split(arg, ",") {
		day, ok := names[name]
		if !ok {
			return nil, fmt.Errorf("weekly on %q: days are mon, tue, wed, thu, fri, sat, sun", name)
		}
		if !seen[day] {
			seen[day] = true
			days = append(days, day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return weekIndex(days[i]) < weekIndex(days[j]) })
	return days, nil
}

func formatWeekdays(days []time.Weekday) string {
	names := make([]string, 0, len(days))
	for _, day := range days {
		names = append(names, strings.ToLower(day.String()[:3]))
	}
	return strings.Join(names, ",")
}

// weekIndex orders a week Monday first, which is how the rules read.
func weekIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

// after finds the first occurrence at or after day, or strictly after it when
// inclusive is false.
func (s schedule) after(day time.Time, inclusive bool) (time.Time, bool) {
	from := day
	if !inclusive {
		from = day.AddDate(0, 0, 1)
	}
	switch {
	case s.daily:
		return from, true
	case len(s.weekdays) > 0:
		for i := 0; i < 7; i++ {
			candidate := from.AddDate(0, 0, i)
			for _, want := range s.weekdays {
				if candidate.Weekday() == want {
					return candidate, true
				}
			}
		}
	case s.monthLast || s.monthDay > 0:
		year, month := day.Year(), day.Month()
		for i := 0; i < 14; i++ {
			y, m := shiftMonth(year, month, i)
			d := daysIn(y, m)
			if !s.monthLast && s.monthDay < d {
				d = s.monthDay
			}
			candidate := date(y, m, d)
			if !candidate.Before(from) {
				return candidate, true
			}
		}
	case s.yearDay > 0:
		for i := 0; i < 5; i++ {
			y := day.Year() + i
			d := s.yearDay
			if last := daysIn(y, s.yearMonth); d > last {
				d = last
			}
			candidate := date(y, s.yearMonth, d)
			if !candidate.Before(from) {
				return candidate, true
			}
		}
	}
	return time.Time{}, false
}

func (s schedule) addInterval(day time.Time) time.Time {
	switch s.unit {
	case "d":
		return day.AddDate(0, 0, s.count)
	case "w":
		return day.AddDate(0, 0, 7*s.count)
	default:
		return addMonths(day, s.count)
	}
}

// addMonths clamps rather than overflowing: a month after the 31st is the end
// of the next month, not the 1st or 3rd of the one after.
func addMonths(day time.Time, n int) time.Time {
	y, m := shiftMonth(day.Year(), day.Month(), n)
	d := day.Day()
	if last := daysIn(y, m); d > last {
		d = last
	}
	return date(y, m, d)
}

func shiftMonth(year int, month time.Month, n int) (int, time.Month) {
	total := int(month) - 1 + n
	return year + total/12, time.Month(total%12 + 1)
}

func daysIn(year int, month time.Month) int {
	return date(year, month+1, 0).Day()
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func parseDate(s string) (time.Time, error) {
	day, err := time.ParseInLocation(DateFormat, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q must be YYYY-MM-DD", s)
	}
	return day, nil
}

func format(day time.Time) string { return day.Format(DateFormat) }
