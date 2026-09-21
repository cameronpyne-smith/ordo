package enrich

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/cameronpyne-smith/ordo/internal/recur"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// Schema is the shape ollama is made to answer in. Every field is required
// and an empty string carries "nothing to say", which small models handle far
// better than an optional key they have to decide to omit.
var Schema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"difficulty": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
		"priority":   map[string]any{"type": "string", "enum": []string{"low", "normal", "high"}},
		"minutes":    map[string]any{"type": "integer"},
		"due":        map[string]any{"type": "string"},
		"recur_kind": map[string]any{"type": "string", "enum": []string{"", "every", "after"}},
		"recur_rule": map[string]any{"type": "string"},
	},
	"required": []string{"difficulty", "priority", "minutes", "due", "recur_kind", "recur_rule"},
}

// maxEstimate rejects a runaway number rather than letting it swallow a whole
// day's budget. Anything genuinely longer than this is several tasks.
const maxEstimate = 8 * 60

const systemPrompt = `You read one item from a personal todo list and extract structured fields from it.
Answer with the JSON object the schema describes and nothing else.

difficulty: how demanding the task is, not how long it takes.
  low means routine, medium means it needs attention, high means it needs real thought or effort.
priority: how much it matters that this one gets done, judged from the task itself.
  high when letting it slip has a real consequence: a deadline, money, health, or
  someone else waiting on it. low when nothing happens if it waits a month.
  normal for the rest. Decide between the three; do not answer normal merely
  because the task does not say which it is.
minutes: how long one go at this takes, in whole minutes, judged from the task itself.
  A quick errand is 5 to 15, something with a bit of setup is 30 to 60, a real session is 90 or
  more. A repeating task means one occurrence, not the whole series. Use 0 when you cannot tell.
due: the date the task is for, as YYYY-MM-DD. Resolve words like "tomorrow", "friday" or
  "next week" against today's date. Use "" when the task names no date.
recur_kind: "every" when the task repeats on a fixed schedule, "after" when it repeats a fixed
  interval after each time it is done, "" when it does not repeat. "every 2w" is a fortnightly
  cycle; "after 2w" is two weeks from the day it was last done.
recur_rule: the schedule, in exactly one of these forms and no other:
  every: daily | weekly on tue | weekly on mon,thu | monthly on 1 | monthly on last |
         yearly on 03-15 | 3d | 2w | 1m
  after: 3d | 2w | 1m
  Use "" when recur_kind is "".

A repeating task carries a recur_rule and no due: the schedule decides the date.`

// taskPrompt gives the model the task in its own words plus the one piece of
// context it cannot have: what today is. Weekday and date both, because half
// the dates people write are weekday names.
func taskPrompt(t *store.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\nTask: %s\n", store.Now().Format("Monday, 2006-01-02"), t.Title)
	if t.Notes != "" {
		fmt.Fprintf(&b, "Notes: %s\n", t.Notes)
	}
	return b.String()
}

type extraction struct {
	Difficulty string `json:"difficulty"`
	Priority   string `json:"priority"`
	Minutes    int    `json:"minutes"`
	Due        string `json:"due"`
	RecurKind  string `json:"recur_kind"`
	RecurRule  string `json:"recur_rule"`
}

// inference keeps the fields the model got right and drops the ones it did
// not. A due date it invented should not cost the difficulty that came back
// in the same answer, and the store would reject the whole write.
func (e extraction) inference(log *slog.Logger, id int64) store.Inference {
	var in store.Inference

	switch store.Difficulty(e.Difficulty) {
	case store.DifficultyLow, store.DifficultyMedium, store.DifficultyHigh:
		in.Difficulty = store.Difficulty(e.Difficulty)
	case "":
	default:
		log.Warn("model returned an unknown difficulty", "id", id, "difficulty", e.Difficulty)
	}

	switch store.Priority(e.Priority) {
	case store.PriorityLow, store.PriorityNormal, store.PriorityHigh:
		in.Priority = store.Priority(e.Priority)
	case "":
	default:
		log.Warn("model returned an unknown priority", "id", id, "priority", e.Priority)
	}

	switch {
	case e.Minutes < 0 || e.Minutes > maxEstimate:
		log.Warn("model returned an unusable estimate", "id", id, "minutes", e.Minutes)
	default:
		in.Estimate = e.Minutes
	}

	if e.Due != "" {
		if _, err := store.ParseDate(e.Due); err != nil {
			log.Warn("model returned an unusable due date", "id", id, "due", e.Due)
		} else {
			in.Due = e.Due
		}
	}

	if e.RecurKind != "" {
		rule, err := recur.Normalise(e.RecurKind, e.RecurRule)
		if err != nil {
			log.Warn("model returned an unusable recurrence", "id", id,
				"kind", e.RecurKind, "rule", e.RecurRule, "error", err)
		} else {
			in.RecurKind, in.RecurRule = store.RecurKind(e.RecurKind), rule
		}
	}
	return in
}
