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
	sectionWaiting  = "waiting"
	sectionDoneDay  = "done today"
	sectionDone     = "done"
)

var sectionOrder = []string{
	sectionOverdue, sectionToday, sectionThisWeek, sectionLater, sectionSomeday, sectionWaiting, sectionDoneDay, sectionDone,
}

const weekAhead = 7

func sectionOf(t api.Task) string {
	if t.Status == "done" {
		return sectionDone
	}
	if t.DoneToday {
		return sectionDoneDay
	}
	// Everything above waiting is something you could pick up now.
	if waiting(t) {
		return sectionWaiting
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

// waiting reports whether an open task cannot be started yet: something it
// waits on is still open, or its start date has not come.
func waiting(t api.Task) bool {
	return t.Status == "open" && (t.Blocked || t.Start > store.Today())
}

// waitingOn is the ids of what still holds a task up.
func waitingOn(t api.Task) string {
	var ids []string
	for _, d := range t.BlockedBy {
		if !d.Done {
			ids = append(ids, fmt.Sprint(d.ID))
		}
	}
	return strings.Join(ids, ", ")
}

// waitPhrase says why a waiting task cannot be started, or nothing.
func waitPhrase(t api.Task) string {
	switch {
	case t.Status != "open":
		return ""
	case t.Blocked:
		return "waits on " + waitingOn(t)
	case t.Start > store.Today():
		return "from " + api.ShowDate(t.Start)
	}
	return ""
}

// deadline is the date the task has to be done by: its own due date, or the
// earlier one a task waiting on it passed down.
func deadline(t api.Task) string {
	if t.EffectiveDue != "" {
		return t.EffectiveDue
	}
	return t.Due
}

// daysUntilDue counts whole days from today to the deadline, negative when
// the date has passed. A task with no date, or one the daemon wrote in a
// shape this build cannot read, is simply undated here.
func daysUntilDue(t api.Task) (int, bool) {
	if deadline(t) == "" {
		return 0, false
	}
	due, err := store.ParseDate(deadline(t))
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
	if wait := waitPhrase(t); wait != "" {
		parts = append(parts, wait)
	}
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
	phrase := dayPhrase(t)
	if t.EffectiveDue != "" {
		phrase += fmt.Sprintf(" so %d can follow in time", t.DueFor)
	}
	return phrase
}

func dayPhrase(t api.Task) string {
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
