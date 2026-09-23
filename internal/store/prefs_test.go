package store

import (
	"errors"
	"testing"
)

// A database nobody has configured still has to schedule sensibly, so the
// defaults are the answer until someone says otherwise.
func TestPreferencesDefaultBeforeAnythingIsWritten(t *testing.T) {
	st := open(t)
	got, err := st.Preferences()
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultPreferences() {
		t.Fatalf("preferences = %+v, want the defaults", got)
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	st := open(t)
	p := DefaultPreferences()
	p.DayStart, p.DayEnd = Clock{6, 30}, Clock{23, 59}
	p.DeepStart, p.DeepEnd = Clock{22, 0}, Clock{23, 59}
	p.BufferMinutes = 5
	p.MaxMinutesDay = 300

	if _, err := st.SetPreferences(p); err != nil {
		t.Fatal(err)
	}
	got, err := st.Preferences()
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("preferences = %+v, want %+v", got, p)
	}
}

// Writing twice must update the row rather than fail on the primary key:
// there is only ever one day shape.
func TestPreferencesOverwrite(t *testing.T) {
	st := open(t)
	p := DefaultPreferences()
	if _, err := st.SetPreferences(p); err != nil {
		t.Fatal(err)
	}
	p.MaxMinutesDay = 90
	if _, err := st.SetPreferences(p); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Preferences()
	if got.MaxMinutesDay != 90 {
		t.Fatalf("max = %d, want the second write to win", got.MaxMinutesDay)
	}
}

func TestPreferencesRejectAnIncoherentDay(t *testing.T) {
	st := open(t)
	cases := []struct {
		name string
		bad  func(*Preferences)
	}{
		{"day ends before it starts", func(p *Preferences) { p.DayEnd = Clock{6, 0} }},
		{"deep work ends before it starts", func(p *Preferences) { p.DeepEnd = Clock{6, 0} }},
		{"deep work outside the day", func(p *Preferences) { p.DeepStart, p.DeepEnd = Clock{5, 0}, Clock{6, 0} }},
		{"negative buffer", func(p *Preferences) { p.BufferMinutes = -1 }},
		{"zero minimum block", func(p *Preferences) { p.MinBlockMinutes = 0 }},
		{"zero daily cap", func(p *Preferences) { p.MaxMinutesDay = 0 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := DefaultPreferences()
			c.bad(&p)
			_, err := st.SetPreferences(p)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseClock(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Clock
	}{
		{"09:00", Clock{9, 0}},
		{" 17:30 ", Clock{17, 30}},
		{"00:00", Clock{0, 0}},
		{"23:59", Clock{23, 59}},
	} {
		got, err := ParseClock(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseClock(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "9", "9am", "25:00", "09:60", "09-00"} {
		if _, err := ParseClock(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseClock(%q) accepted it", bad)
		}
	}
}

func TestClockOnPlacesItInTheDay(t *testing.T) {
	day, err := ParseDate("2026-09-21")
	if err != nil {
		t.Fatal(err)
	}
	got := Clock{9, 30}.On(day)
	if got.Format("2006-01-02 15:04") != "2026-09-21 09:30" {
		t.Fatalf("got %s", got)
	}
	if got.Location() != Location {
		t.Errorf("location = %v, want the daemon's", got.Location())
	}
}
