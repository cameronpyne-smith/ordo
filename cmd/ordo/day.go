package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func newTodayCmd(configPath *string) *cobra.Command {
	var day string
	var why bool
	cmd := &cobra.Command{
		Use:   "today",
		Short: "Plan a day from its free time and the list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			if day, err = store.ReadDate(day); err != nil {
				return err
			}
			plan, err := c.Today(day)
			if err != nil {
				return err
			}
			printDay(cmd.OutOrStdout(), plan, why)
			return nil
		},
	}
	cmd.Flags().StringVar(&day, "day", "", "the day to plan, 25/09/26 or 2026-09-25 (default today)")
	cmd.Flags().BoolVar(&why, "why", false, "show the reason for each block and what did not fit")
	return cmd
}

func printDay(w io.Writer, plan *api.TodayResponse, why bool) {
	fmt.Fprintf(w, "%s  %d of %d minutes planned\n", plan.Date, plan.PlannedMinutes, plan.BudgetMinutes)
	if plan.Tally != nil {
		fmt.Fprintf(w, "%s so far\n", plan.Tally.Said())
	}
	if plan.CalendarError != "" {
		fmt.Fprintf(w, "the calendar could not be read: %s\n", plan.CalendarError)
	}
	fmt.Fprintln(w)

	if len(plan.Blocks) == 0 && len(plan.Busy) == 0 {
		fmt.Fprintln(w, "nothing to do and nothing in the way")
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range timeline(plan) {
		fmt.Fprintln(tw, row)
	}
	tw.Flush()
	printLogged(w, plan.Done)

	if !why {
		return
	}
	if len(plan.Blocks) > 0 {
		fmt.Fprintln(w)
		for _, b := range plan.Blocks {
			fmt.Fprintf(w, "%s  %s\n", b.Start, b.Reason)
		}
	}
	if len(plan.Skipped) > 0 {
		fmt.Fprintf(w, "\nnot today\n")
		for _, s := range plan.Skipped {
			fmt.Fprintf(w, "  %-4d %s — %s\n", s.Task.ID, s.Task.Title, s.Reason)
		}
	}
	if len(plan.Free) > 0 {
		fmt.Fprintf(w, "\nstill free\n")
		for _, f := range plan.Free {
			fmt.Fprintf(w, "  %s-%s  %d min\n", f.Start, f.End, f.Minutes)
		}
	}
}

// printLogged is what the day has already got done, under what is left of
// it, so the day reads as a whole.
func printLogged(w io.Writer, logged []api.Logged) {
	if len(logged) == 0 {
		return
	}
	fmt.Fprintf(w, "\ndone today\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, l := range logged {
		mark, length := "✓", ""
		if l.Partial {
			mark, length = " ", "worked "
		}
		if l.Minutes > 0 {
			length += minutesText(l.Minutes)
		}
		fmt.Fprintf(tw, "  %s %s\t%4d\t%s\t%s\n", mark, l.At, l.ID, l.Title, strings.TrimSpace(length))
	}
	tw.Flush()
}

// timeline interleaves the blocks with what the calendar already had, so the
// day reads top to bottom as it will actually happen rather than as two
// lists that have to be merged by eye.
func timeline(plan *api.TodayResponse) []string {
	type entry struct {
		at   string
		line string
	}
	var rows []entry
	for _, b := range plan.Blocks {
		title := b.Task.Title
		if b.Task.PinnedOn != "" {
			title = "📌 " + title
		}
		length := minutesText(b.Minutes)
		if b.Left > 0 {
			length += " of " + minutesText(b.Left) + " left"
		}
		rows = append(rows, entry{b.Start, fmt.Sprintf("%s-%s\t%4d\t%s\t%s",
			b.Start, b.End, b.Task.ID, title, length)})
	}
	for _, b := range plan.Busy {
		summary := b.Summary
		if summary == "" {
			summary = "busy"
		}
		rows = append(rows, entry{b.Start, fmt.Sprintf("%s-%s\t\t%s\t%s",
			b.Start, b.End, summary, "(calendar)")})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at < rows[j].at })

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.line)
	}
	return out
}

func minutesText(n int) string { return api.Hours(n) }

func newPinCmd(configPath *string) *cobra.Command {
	var day string
	cmd := &cobra.Command{
		Use:   "pin <id>",
		Short: "Claim a task for a day, ahead of whatever the order would have chosen",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			if day, err = store.ReadDate(day); err != nil {
				return err
			}
			t, err := c.Pin(id, day)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pinned %d to %s: %s\n", t.ID, t.PinnedOn, t.Title)
			return nil
		},
	}
	cmd.Flags().StringVar(&day, "day", "", "the day to pin it to, 25/09/26 or 2026-09-25 (default today)")
	return cmd
}

func newUnpinCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "unpin <id>",
		Short: "Release a task from the day it was pinned to",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			t, err := c.Unpin(id)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unpinned %d: %s\n", t.ID, t.Title)
			return nil
		},
	}
}

// newPrefsCmd reads with no arguments and writes with key=value pairs, the
// same shape as `ordo set`, so there is one way to change a thing in ordo.
func newPrefsCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "prefs [key=value ...]",
		Short: "Show or change the shape of the day the scheduler plans into",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				p, err := c.Preferences()
				if err != nil {
					return err
				}
				printPrefs(cmd.OutOrStdout(), p)
				return nil
			}
			req, err := prefsRequest(args)
			if err != nil {
				return err
			}
			p, err := c.SetPreferences(req)
			if err != nil {
				return err
			}
			printPrefs(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func prefsRequest(args []string) (api.PreferencesRequest, error) {
	var req api.PreferencesRequest
	texts := map[string]**string{
		"day_start":  &req.DayStart,
		"day_end":    &req.DayEnd,
		"deep_start": &req.DeepStart,
		"deep_end":   &req.DeepEnd,
	}
	numbers := map[string]**int{
		"buffer_minutes":      &req.BufferMinutes,
		"min_block_minutes":   &req.MinBlockMinutes,
		"max_minutes_per_day": &req.MaxMinutesDay,
		"max_block_minutes":   &req.MaxBlockMinutes,
	}
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return req, fmt.Errorf("expected key=value, got %q", arg)
		}
		key = strings.TrimSpace(key)
		if into, ok := texts[key]; ok {
			v := value
			*into = &v
			continue
		}
		if into, ok := numbers[key]; ok {
			n, err := parseMinutes(value)
			if err != nil {
				return req, fmt.Errorf("%s: %w", key, err)
			}
			*into = &n
			continue
		}
		return req, fmt.Errorf("unknown preference %q", key)
	}
	return req, nil
}

func parseMinutes(s string) (int, error) {
	n := 0
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("expected a number of minutes")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("expected a number of minutes, got %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func printPrefs(w io.Writer, p *api.Preferences) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range [][2]string{
		{"day_start", p.DayStart},
		{"day_end", p.DayEnd},
		{"deep_start", p.DeepStart},
		{"deep_end", p.DeepEnd},
		{"buffer_minutes", fmt.Sprint(p.BufferMinutes)},
		{"min_block_minutes", fmt.Sprint(p.MinBlockMinutes)},
		{"max_minutes_per_day", fmt.Sprint(p.MaxMinutesDay)},
		{"max_block_minutes", fmt.Sprint(p.MaxBlockMinutes)},
	} {
		fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
	}
	tw.Flush()
}
