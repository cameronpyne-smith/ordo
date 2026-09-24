package api

import (
	"fmt"
	"strconv"
	"strings"
)

// Said is an outcome in the words every client uses, one phrase per thing
// that changed, for a line after "done".
func (o *Outcome) Said() []string {
	if o == nil {
		return nil
	}
	var out []string
	var now []string
	for _, f := range o.Freed {
		if f.Start == "" {
			now = append(now, fmt.Sprintf("%d %s", f.ID, f.Title))
		}
	}
	if len(now) > 0 {
		out = append(out, "can start now: "+strings.Join(now, ", "))
	}
	for _, f := range o.Freed {
		if f.Start != "" {
			out = append(out, fmt.Sprintf("%d %s can start from %s", f.ID, f.Title, f.Start))
		}
	}
	if n := len(o.WaitAgain); n > 0 {
		ids := make([]string, 0, n)
		for _, d := range o.WaitAgain {
			ids = append(ids, strconv.FormatInt(d.ID, 10))
		}
		verb := "wait"
		if n == 1 {
			verb = "waits"
		}
		out = append(out, fmt.Sprintf("%s %s again", strings.Join(ids, ", "), verb))
	}
	switch {
	case o.StreakWas > 0:
		out = append(out, fmt.Sprintf("back to 1 (was %d)", o.StreakWas))
	case o.Streak >= 2:
		out = append(out, fmt.Sprintf("%d in a row", o.Streak))
	}
	if o.Chain > 0 {
		out = append(out, fmt.Sprintf("that closes a chain of %d", o.Chain))
	}
	if o.Note != "" {
		out = append(out, fmt.Sprintf("that's everything for [[%s]] — %d of %d done", o.Note, o.NoteDone, o.NoteDone))
	}
	return out
}

// Hours is minutes the way ordo says them: 40 min, 2h, 2h10.
func Hours(minutes int) string {
	switch {
	case minutes < 60:
		return fmt.Sprintf("%d min", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dh%02d", minutes/60, minutes%60)
}

// Said is a tally in a few words: "3 done · 2h10", or only the time when
// nothing has been finished yet.
func (t *Tally) Said() string {
	if t == nil {
		return ""
	}
	switch {
	case t.Done == 0:
		return "worked " + Hours(t.Minutes)
	case t.Minutes == 0:
		return fmt.Sprintf("%d done", t.Done)
	}
	return fmt.Sprintf("%d done · %s", t.Done, Hours(t.Minutes))
}
