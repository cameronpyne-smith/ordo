package enrich

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// A title keeps the date it was added with only while nothing else says it:
// once the due date or schedule holds it, "by tomorrow" is noise, and wrong
// by the next day. The model proposes the shorter title, but it is trusted
// only to delete, and only to delete words that could be nothing but a date
// or a schedule, so a misreading costs the trim and never the task's words.

// maxCut is the longest phrase a date or schedule takes: "by the end of next
// week" is six words.
const maxCut = 6

// connectors and whens are everything a cut phrase may be made of, besides
// numbers. A phrase with any other word in it is about more than when, so
// it stays; one with nothing but connectors names no date at all.
var connectors, whens = words(`
	by on at in the of a an and or before until till til due from starting start after
	every each other next this last end latest later than no for within
	st nd rd th one two three four five six seven eight nine ten eleven twelve couple few`), words(`
	today tonight tomorrow tmrw tmr weekend weekends weekday weekdays fortnight
	day days week weeks month months year years
	daily weekly fortnightly monthly yearly annually
	morning afternoon evening night noon midday eod eow am pm
	monday tuesday wednesday thursday friday saturday sunday
	mon tue tues wed weds thu thur thurs fri sat sun
	january february march april may june july august september october november december
	jan feb mar apr jun jul aug sep sept oct nov dec`)

func words(list string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(list) {
		out[w] = true
	}
	return out
}

// shorten is the model's proposed title if it is the original with a single
// date phrase cut out, and "" otherwise. What is kept is the original's own
// text, so spelling and case come from what was typed, not from the model.
func shorten(original, proposed string) string {
	said, kept := strings.Fields(original), strings.Fields(proposed)
	cut := len(said) - len(kept)
	if len(kept) == 0 || cut < 1 || cut > maxCut {
		return ""
	}
	for i := 0; i <= len(kept); i++ {
		if !sameWords(said[:i], kept[:i]) || !sameWords(said[i+cut:], kept[i:]) {
			continue
		}
		if !aboutWhen(said[i : i+cut]) {
			continue
		}
		title := strings.Join(append(append([]string{}, said[:i]...), said[i+cut:]...), " ")
		return tidy(title, original)
	}
	return ""
}

func sameWords(a, b []string) bool {
	for i := range a {
		if bare(a[i]) != bare(b[i]) {
			return false
		}
	}
	return true
}

func aboutWhen(phrase []string) bool {
	named := false
	for _, w := range phrase {
		w = bare(w)
		switch {
		case whens[w] || strings.ContainsFunc(w, unicode.IsDigit):
			named = true
		case !connectors[w]:
			return false
		}
	}
	return named
}

// bare is a word as compared: lower case, without the punctuation around it.
func bare(w string) string {
	return strings.ToLower(strings.TrimFunc(w, func(r rune) bool { return unicode.IsPunct(r) }))
}

// tidy drops the comma or dash a cut at the end leaves dangling, and keeps a
// capital the cut took off the front.
func tidy(title, original string) string {
	title = strings.TrimRight(title, " ,;:-–")
	first, size := utf8.DecodeRuneInString(title)
	if lead, _ := utf8.DecodeRuneInString(original); unicode.IsUpper(lead) && unicode.IsLower(first) {
		title = string(unicode.ToUpper(first)) + title[size:]
	}
	return title
}
