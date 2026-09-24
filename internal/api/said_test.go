package api

import (
	"strings"
	"testing"
)

func TestWhatAnOutcomeSays(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    *Outcome
		want string
	}{
		{"nothing", nil, ""},
		{"freed", &Outcome{Freed: []Freed{{ID: 2, Title: "Move wardrobe"}, {ID: 3, Title: "Paint", Start: "2026-10-01"}, {ID: 1, Title: "Move sofa"}}},
			"can start now: 2 Move wardrobe, 1 Move sofa | 3 Paint can start from 2026-10-01"},
		{"one waits again", &Outcome{WaitAgain: []Dep{{ID: 2}}}, "2 waits again"},
		{"two wait again", &Outcome{WaitAgain: []Dep{{ID: 2}, {ID: 1}}}, "2, 1 wait again"},
		{"a first time", &Outcome{Streak: 1}, ""},
		{"a run", &Outcome{Streak: 12}, "12 in a row"},
		{"a run broken", &Outcome{Streak: 1, StreakWas: 14}, "back to 1 (was 14)"},
		{"a chain and a note", &Outcome{Chain: 4, Note: "latent", NoteDone: 6},
			"that closes a chain of 4 | that's everything for [[latent]] — 6 of 6 done"},
	} {
		if got := strings.Join(tc.o.Said(), " | "); got != tc.want {
			t.Errorf("%s: said %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWhatATallySays(t *testing.T) {
	for _, tc := range []struct {
		t    *Tally
		want string
	}{
		{nil, ""},
		{&Tally{Done: 3, Minutes: 130}, "3 done · 2h10"},
		{&Tally{Done: 2}, "2 done"},
		{&Tally{Minutes: 30}, "worked 30 min"},
		{&Tally{Done: 1, Minutes: 120}, "1 done · 2h"},
	} {
		if got := tc.t.Said(); got != tc.want {
			t.Errorf("said %q, want %q", got, tc.want)
		}
	}
}
