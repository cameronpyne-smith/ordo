package recur

import "testing"

func TestNormalise(t *testing.T) {
	cases := []struct {
		kind, rule, want string
	}{
		{KindEvery, "daily", "daily"},
		{KindEvery, "DAILY", "daily"},
		{KindEvery, "weekly on tue", "weekly on tue"},
		{KindEvery, "Weekly on THU, mon", "weekly on mon,thu"},
		{KindEvery, "weekly on tue,tue", "weekly on tue"},
		{KindEvery, "monthly on 1", "monthly on 1"},
		{KindEvery, "monthly on last", "monthly on last"},
		{KindEvery, "yearly on 03-15", "yearly on 03-15"},
		{KindEvery, "2w", "2w"},
		{KindEvery, " 2W ", "2w"},
		{KindEvery, "10d", "10d"},
		{KindAfter, "3d", "3d"},
		{KindAfter, " 2W ", "2w"},
		{KindAfter, "1m", "1m"},
	}
	for _, c := range cases {
		got, err := Normalise(c.kind, c.rule)
		if err != nil {
			t.Errorf("Normalise(%q, %q): %v", c.kind, c.rule, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalise(%q, %q) = %q, want %q", c.kind, c.rule, got, c.want)
		}
	}
}

func TestNormaliseRejects(t *testing.T) {
	cases := []struct{ kind, rule string }{
		{"", "daily"},
		{"sometimes", "daily"},
		{KindEvery, ""},
		{KindEvery, "daily on tue"},
		{KindEvery, "weekly tue"},
		{KindEvery, "weekly on tues"},
		{KindEvery, "weekly on mon,funday"},
		{KindEvery, "monthly on 0"},
		{KindEvery, "monthly on 32"},
		{KindEvery, "monthly on first"},
		{KindEvery, "yearly on 15-03"},
		{KindEvery, "hourly on 3"},
		{KindEvery, "3y"},
		{KindEvery, "0w"},
		{KindEvery, "fortnightly"},
		{KindEvery, "every 2w"},
		{KindAfter, ""},
		{KindAfter, "3"},
		{KindAfter, "d"},
		{KindAfter, "0d"},
		{KindAfter, "-1d"},
		{KindAfter, "3y"},
		{KindAfter, "three days"},
	}
	for _, c := range cases {
		if got, err := Normalise(c.kind, c.rule); err == nil {
			t.Errorf("Normalise(%q, %q) = %q, want an error", c.kind, c.rule, got)
		}
	}
}

func TestNext(t *testing.T) {
	cases := []struct {
		name           string
		kind, rule, on string
		want           string
	}{
		{"daily rolls one day", KindEvery, "daily", "2026-09-21", "2026-09-22"},
		{"daily crosses a month", KindEvery, "daily", "2026-09-30", "2026-10-01"},
		{"weekly from the day before", KindEvery, "weekly on tue", "2026-09-21", "2026-09-22"},
		{"weekly from the day itself skips a week", KindEvery, "weekly on tue", "2026-09-22", "2026-09-29"},
		{"weekly picks the nearer of two days", KindEvery, "weekly on mon,thu", "2026-09-22", "2026-09-24"},
		{"weekly wraps to the next week", KindEvery, "weekly on mon,thu", "2026-09-24", "2026-09-28"},
		{"monthly moves to next month", KindEvery, "monthly on 1", "2026-09-01", "2026-10-01"},
		{"monthly stays in month when the day is ahead", KindEvery, "monthly on 15", "2026-09-01", "2026-09-15"},
		{"monthly clamps a short month", KindEvery, "monthly on 31", "2026-01-31", "2026-02-28"},
		{"monthly on last", KindEvery, "monthly on last", "2026-09-30", "2026-10-31"},
		{"monthly on last from mid month", KindEvery, "monthly on last", "2026-02-10", "2026-02-28"},
		{"yearly ahead in the same year", KindEvery, "yearly on 03-15", "2026-01-01", "2026-03-15"},
		{"yearly rolls to the next year", KindEvery, "yearly on 03-15", "2026-03-15", "2027-03-15"},
		{"yearly clamps 29 February", KindEvery, "yearly on 02-29", "2026-01-01", "2026-02-28"},
		{"after days", KindAfter, "3d", "2026-09-21", "2026-09-24"},
		{"after weeks", KindAfter, "2w", "2026-09-21", "2026-10-05"},
		{"after months", KindAfter, "1m", "2026-09-21", "2026-10-21"},
		{"after a month clamps", KindAfter, "1m", "2026-01-31", "2026-02-28"},
		{"after months crosses a year", KindAfter, "3m", "2026-11-30", "2027-02-28"},
		{"every fortnight", KindEvery, "2w", "2026-09-21", "2026-10-05"},
		{"every interval clamps a month", KindEvery, "1m", "2026-01-31", "2026-02-28"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Next(c.kind, c.rule, c.on)
			if err != nil {
				t.Fatalf("Next(%q, %q, %q): %v", c.kind, c.rule, c.on, err)
			}
			if got != c.want {
				t.Errorf("Next(%q, %q, %q) = %s, want %s", c.kind, c.rule, c.on, got, c.want)
			}
		})
	}
}

func TestFirst(t *testing.T) {
	cases := []struct {
		name           string
		kind, rule, on string
		want           string
	}{
		{"daily starts today", KindEvery, "daily", "2026-09-21", "2026-09-21"},
		{"weekly starts today when today matches", KindEvery, "weekly on tue", "2026-09-22", "2026-09-22"},
		{"weekly waits for the day", KindEvery, "weekly on tue", "2026-09-21", "2026-09-22"},
		{"monthly starts today when today matches", KindEvery, "monthly on 1", "2026-09-01", "2026-09-01"},
		{"monthly waits for the day", KindEvery, "monthly on 1", "2026-09-02", "2026-10-01"},
		{"yearly starts today when today matches", KindEvery, "yearly on 03-15", "2026-03-15", "2026-03-15"},
		{"after is due straight away", KindAfter, "3d", "2026-09-21", "2026-09-21"},
		{"every interval starts today", KindEvery, "2w", "2026-09-21", "2026-09-21"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := First(c.kind, c.rule, c.on)
			if err != nil {
				t.Fatalf("First(%q, %q, %q): %v", c.kind, c.rule, c.on, err)
			}
			if got != c.want {
				t.Errorf("First(%q, %q, %q) = %s, want %s", c.kind, c.rule, c.on, got, c.want)
			}
		})
	}
}

// A rule that never repeats a date is the failure that would matter most: a
// chore that silently stops. Walk a year of each rule and check it always
// moves forward.
func TestNextAlwaysAdvances(t *testing.T) {
	rules := []struct{ kind, rule string }{
		{KindEvery, "daily"},
		{KindEvery, "weekly on tue"},
		{KindEvery, "weekly on mon,thu"},
		{KindEvery, "monthly on 1"},
		{KindEvery, "monthly on 31"},
		{KindEvery, "monthly on last"},
		{KindEvery, "yearly on 02-29"},
		{KindAfter, "3d"},
		{KindAfter, "2w"},
		{KindAfter, "1m"},
		{KindEvery, "2w"},
		{KindEvery, "1m"},
	}
	for _, r := range rules {
		at := "2026-01-01"
		for i := 0; i < 365; i++ {
			next, err := Next(r.kind, r.rule, at)
			if err != nil {
				t.Fatalf("%s %s at %s: %v", r.kind, r.rule, at, err)
			}
			if next <= at {
				t.Fatalf("%s %s: %s did not advance past %s", r.kind, r.rule, next, at)
			}
			at = next
		}
	}
}

func TestBadDate(t *testing.T) {
	if _, err := Next(KindEvery, "daily", "21-09-2026"); err == nil {
		t.Error("Next with a non-ISO date: want an error")
	}
}

// Interval is what display uses to tell "2w" from "weekly on tue", since one
// of them says what it means and the other does not.
func TestInterval(t *testing.T) {
	cases := []struct {
		kind, rule string
		want       bool
	}{
		{KindEvery, "2w", true},
		{KindEvery, "1m", true},
		{KindAfter, "3d", true},
		{KindEvery, "daily", false},
		{KindEvery, "weekly on tue", false},
		{KindEvery, "monthly on last", false},
		{KindEvery, "nonsense", false},
	}
	for _, c := range cases {
		if got := Interval(c.kind, c.rule); got != c.want {
			t.Errorf("Interval(%q, %q) = %v, want %v", c.kind, c.rule, got, c.want)
		}
	}
}
