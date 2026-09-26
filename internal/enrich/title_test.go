package enrich

import "testing"

// The model may only delete, and only a phrase that says when; anything
// else keeps the title as it was typed.
func TestShorten(t *testing.T) {
	for _, c := range []struct{ original, proposed, want string }{
		{"Email fred by tomorrow", "Email fred", "Email fred"},
		{"Put the bins out every tuesday", "Put the bins out", "Put the bins out"},
		{"take the bins out on tuesday", "take the bins out", "take the bins out"},
		{"By friday, send the CV", "send the CV", "Send the CV"},
		{"Email fred, by tomorrow.", "Email fred", "Email fred"},
		{"Email fred by 3 oct about the budget", "Email fred about the budget", "Email fred about the budget"},
		{"Finish the report by the end of next week", "Finish the report", "Finish the report"},
		{"Email fred by tomorrow", "Email Fred", "Email fred"},
		{"Email fred by tomorrow", "Message fred", ""},
		{"Email fred about tomorrow's meeting", "Email fred", ""},
		{"Email fred and the team", "Email fred", ""},
		{"Tomorrow", "", ""},
		{"Email fred by tomorrow", "Email fred by tomorrow", ""},
	} {
		if got := shorten(c.original, c.proposed); got != c.want {
			t.Errorf("shorten(%q, %q) = %q, want %q", c.original, c.proposed, got, c.want)
		}
	}
}
