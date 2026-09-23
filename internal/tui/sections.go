package tui

import (
	"fmt"
	"strings"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/recur"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// The sections are a grouping of the order the daemon already returns, not a
// re-sort of it: a task falls into the first one it qualifies for, and the
// list inside each stays in the daemon's order. So the terminal can never
// disagree with the CLI about what comes next.
const (
	sectionOverdue  = "overdue"
	sectionToday    = "today"
	sectionThisWeek = "this week"
	sectionLater    = "later"
	sectionSomeday  = "someday"
	sectionDone     = "done"
)

var sectionOrder = []string{
	sectionOverdue, sectionToday, sectionThisWeek, sectionLater, sectionSomeday, sectionDone,
}

const weekAhead = 7

func sectionOf(t api.Task) string {
	if t.Status == "done" {
		return sectionDone
	}
	days, dated := daysUntilDue(t)
	switch {
	case !dated:
		return sectionSomeday
	case days < 0:
		return sectionOverdue
	case days == 0:
		return sectionToday
	case days <= weekAhead:
		return sectionThisWeek
	default:
		return sectionLater
	}
}

// daysUntilDue counts whole days from today to the due date, negative when
// the date has passed. A task with no date, or one the daemon wrote in a
// shape this build cannot read, is simply undated here.
func daysUntilDue(t api.Task) (int, bool) {
	if t.Due == "" {
		return 0, false
	}
	due, err := store.ParseDate(t.Due)
	if err != nil {
		return 0, false
	}
	today, err := store.ParseDate(store.Today())
	if err != nil {
		return 0, false
	}
	return int(due.Sub(today).Hours() / 24), true
}

// why states, in the order the daemon sorts by, the fields that put this task
// where it is. It is the answer to "why is this one here" without having to
// know the ordering rule.
func why(t api.Task) string {
	var parts []string
	parts = append(parts, duePhrase(t))
	if t.Priority != "" && t.Priority != "normal" {
		parts = append(parts, t.Priority+" priority")
	}
	if t.Difficulty != "" {
		parts = append(parts, t.Difficulty+" difficulty")
	}
	if store.QuickWin(store.Difficulty(t.Difficulty), t.EstimateMinutes, t.RemainingMinutes) {
		parts = append(parts, "a quick win")
	}
	if t.Recur != nil {
		parts = append(parts, "repeats "+recurPhrase(*t.Recur))
	}
	switch {
	case t.EstimateMinutes > 0 && t.RemainingMinutes > 0:
		parts = append(parts, fmt.Sprintf("about %d min, %d left", t.EstimateMinutes, t.RemainingMinutes))
	case t.RemainingMinutes > 0:
		parts = append(parts, fmt.Sprintf("%d min left", t.RemainingMinutes))
	case t.EstimateMinutes > 0:
		parts = append(parts, fmt.Sprintf("about %d min", t.EstimateMinutes))
	}
	if !t.Enriched {
		parts = append(parts, "still being read by the model")
	}
	return strings.Join(parts, " · ")
}

func duePhrase(t api.Task) string {
	days, dated := daysUntilDue(t)
	switch {
	case !dated:
		return "no due date, so it sorts after everything dated"
	case days < -1:
		return fmt.Sprintf("overdue by %d days", -days)
	case days == -1:
		return "overdue since yesterday"
	case days == 0:
		return "due today"
	case days == 1:
		return "due tomorrow"
	default:
		return fmt.Sprintf("due in %d days", days)
	}
}

func recurPhrase(r api.Recur) string {
	if r.Kind == "after" {
		return r.Rule + " after each completion"
	}
	if recur.Interval(r.Kind, r.Rule) {
		return "every " + r.Rule
	}
	return r.Rule
}
