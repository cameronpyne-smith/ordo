package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
	"github.com/cameronpyne-smith/ordo/internal/recur"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func newClient(configPath string) (*client.Client, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return client.New(cfg.ServerURL(), cfg.Token), nil
}

func newListCmd(configPath *string) *cobra.Command {
	var f client.Filter
	var done, all bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks in priority order",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			switch {
			case all:
				f.Status = "all"
			case done:
				f.Status = string(store.StatusDone)
			}
			resp, err := c.List(f)
			if err != nil {
				return err
			}
			printTasks(cmd.OutOrStdout(), resp.Tasks)
			if resp.Today != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s today\n", resp.Today.Said())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&done, "done", false, "list completed tasks instead of open ones")
	cmd.Flags().BoolVar(&all, "all", false, "list every task regardless of status")
	cmd.Flags().BoolVar(&f.Overdue, "overdue", false, "only tasks past their due date")
	cmd.Flags().BoolVar(&f.Linked, "linked", false, "only tasks linked to a mnemo note")
	cmd.Flags().StringVar(&f.Note, "note", "", "only tasks linked to this note, by slug")
	cmd.Flags().BoolVar(&f.Recurring, "recurring", false, "only tasks that repeat")
	cmd.Flags().StringVar(&f.Difficulty, "difficulty", "", "low, medium or high")
	cmd.Flags().StringVar(&f.Priority, "priority", "", "low, normal or high")
	cmd.Flags().IntVar(&f.Limit, "limit", 0, "show at most this many")
	return cmd
}

func newAddCmd(configPath *string) *cobra.Command {
	var req api.CreateRequest
	var every, after, waitsOn string

	cmd := &cobra.Command{
		Use:   "add <title...>",
		Short: "Add a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case every != "" && after != "":
				return fmt.Errorf("a task repeats on a schedule or after an interval, not both")
			case every != "":
				req.RecurKind, req.RecurRule = string(store.RecurEvery), every
			case after != "":
				req.RecurKind, req.RecurRule = string(store.RecurAfter), after
			}
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			if req.Due, err = store.ReadDate(req.Due); err != nil {
				return err
			}
			if req.Start, err = store.ReadDate(req.Start); err != nil {
				return err
			}
			if req.BlockedBy, err = parseIDs(waitsOn); err != nil {
				return err
			}
			req.Title = strings.Join(args, " ")
			t, err := c.Create(req)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %d: %s%s\n", t.ID, t.Title, dueSuffix(t))
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Notes, "notes", "", "a one-liner worth keeping with the task")
	cmd.Flags().StringVar(&req.Due, "due", "", "due date, 25/09/26 or 2026-09-25")
	cmd.Flags().StringVar(&req.Start, "start", "", "not before this date, 25/09/26 or 2026-09-25")
	cmd.Flags().StringVar(&waitsOn, "waits-on", "", "ids of tasks that have to be finished first: 10,11")
	cmd.Flags().StringVar(&req.Difficulty, "difficulty", "", "low, medium or high")
	cmd.Flags().StringVar(&req.Priority, "priority", "", "low, normal or high")
	cmd.Flags().IntVar(&req.EstimateMinutes, "estimate", 0, "estimated minutes")
	cmd.Flags().StringVar(&every, "every", "", `repeat on a schedule: daily, "weekly on tue", "weekly on mon,thu", "monthly on 1", "monthly on last", "yearly on 03-15", or a fixed cycle: 2w`)
	cmd.Flags().StringVar(&after, "after", "", "repeat an interval after each completion: 3d, 2w, 1m")
	cmd.Flags().StringVar(&req.MnemoSlug, "link", "", "slug of the mnemo note this task is about")
	return cmd
}

func newDoneCmd(configPath *string) *cobra.Command {
	var minutes int

	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Complete a task",
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
			t, err := c.Done(id, minutes)
			if err != nil {
				return err
			}
			if t.Recur != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "done %d: %s (next due %s)\n", t.ID, t.Title, api.ShowDate(t.Due))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "done %d: %s\n", t.ID, t.Title)
			}
			printSaid(cmd.OutOrStdout(), t.Outcome)
			return nil
		},
	}
	cmd.Flags().IntVar(&minutes, "minutes", 0, "how long it actually took")
	return cmd
}

func newWorkCmd(configPath *string) *cobra.Command {
	var left int

	cmd := &cobra.Command{
		Use:   "work <id> <minutes>",
		Short: "Log a session on a task that leaves some of it still to do",
		Long: "Log a session on a task without finishing it. What is left becomes what was left\n" +
			"less the minutes, unless --left says otherwise; --left 0 finishes the task.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			minutes, err := parseMinutes(args[1])
			if err != nil {
				return err
			}
			var rest *int
			if cmd.Flags().Changed("left") {
				rest = &left
			}
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			t, err := c.Work(id, minutes, rest)
			if err != nil {
				return err
			}
			if t.Status == string(store.StatusDone) {
				fmt.Fprintf(cmd.OutOrStdout(), "done %d: %s\n", t.ID, t.Title)
				printSaid(cmd.OutOrStdout(), t.Outcome)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged %d: %s (%s left)\n", t.ID, t.Title, minutesText(t.RemainingMinutes))
			return nil
		},
	}
	cmd.Flags().IntVar(&left, "left", 0, "minutes still to do afterwards, when it is not simply what was left less these")
	return cmd
}

func newSetCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "set <id> <key=value>...",
		Short: "Change fields on a task; an empty value clears one",
		Long: "Change fields on a task. Keys: title, notes, status, difficulty, priority, due,\n" +
			"start, waits_on, estimate, left, every, after. left is what remains of a task\n" +
			"already started. start holds it out of the plan until that day. waits_on is the\n" +
			"whole list of tasks it waits on, waits_on=10,11, replacing what it had.\n" +
			"An empty value clears the field, for example due= or waits_on=.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			req, err := parseEdits(args[1:])
			if err != nil {
				return err
			}
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			t, err := c.Edit(id, req)
			if err != nil {
				return err
			}
			printTasks(cmd.OutOrStdout(), []api.Task{*t})
			return nil
		},
	}
}

func newUndoCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "undo <id>",
		Short: "Take back the most recent completion or session on a task",
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
			t, err := c.Undo(id)
			if err != nil {
				return err
			}
			if t.RemainingMinutes > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "undone %d: %s (%s left)\n", t.ID, t.Title, minutesText(t.RemainingMinutes))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "undone %d: %s\n", t.ID, t.Title)
			}
			printSaid(cmd.OutOrStdout(), t.Outcome)
			return nil
		},
	}
}

func newEnrichCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "enrich <id>",
		Short: "Read a task with the local model again and fill in what is still unset",
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
			t, err := c.Enrich(id)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "queued %d: %s\n", t.ID, t.Title)
			return nil
		},
	}
}

func newRemoveCmd(configPath *string) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a task permanently, with its completion history",
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
			t, err := c.Get(id)
			if err != nil {
				return err
			}
			if !force {
				fmt.Fprintf(cmd.OutOrStdout(), "delete %d: %s? this cannot be undone [y/N] ", t.ID, t.Title)
				var answer string
				fmt.Fscanln(cmd.InOrStdin(), &answer)
				if !strings.EqualFold(strings.TrimSpace(answer), "y") {
					fmt.Fprintln(cmd.OutOrStdout(), "cancelled")
					return nil
				}
			}
			if _, err := c.Delete(id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %d\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation")
	return cmd
}

func newStatusCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show counts from the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			s, err := c.Status()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "today      %s\nopen       %d\noverdue    %d\ndone       %d\nunenriched %d\n",
				s.Today, s.Open, s.Overdue, s.Done, s.Unenriched)
			return nil
		},
	}
}

func newBackupCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Snapshot the database into backup_dir; run on the box",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if cfg.BackupDir == "" {
				return fmt.Errorf("backup_dir is not set in the config")
			}
			dbPath, err := cfg.DBPath()
			if err != nil {
				return err
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()
			path, err := st.Backup(cfg.BackupDir, backupsKept)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}
}

func parseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("task id %q must be a number", raw)
	}
	return id, nil
}

// parseIDs reads a list of task ids as typed: commas, spaces or both.
func parseIDs(raw string) ([]int64, error) {
	ids := []int64{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		id, err := parseID(part)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseEdits(pairs []string) (api.EditRequest, error) {
	var req api.EditRequest
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return req, fmt.Errorf("expected key=value, got %q", pair)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "title":
			req.Title = &value
		case "notes":
			req.Notes = &value
		case "status":
			req.Status = &value
		case "difficulty":
			req.Difficulty = &value
		case "priority":
			req.Priority = &value
		case "due":
			due, err := store.ReadDate(value)
			if err != nil {
				return req, err
			}
			req.Due = &due
		case "start":
			start, err := store.ReadDate(value)
			if err != nil {
				return req, err
			}
			req.Start = &start
		case "waits_on", "waits-on", "blocked_by":
			ids, err := parseIDs(value)
			if err != nil {
				return req, err
			}
			req.BlockedBy = &ids
		case "every", "after":
			kind := key
			if value == "" {
				kind = ""
			}
			req.RecurKind, req.RecurRule = &kind, &value
		case "estimate", "estimate_minutes":
			minutes := 0
			if value != "" {
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return req, fmt.Errorf("estimate %q must be a number of minutes", value)
				}
				minutes = parsed
			}
			req.EstimateMinutes = &minutes
		case "left", "remaining", "remaining_minutes":
			minutes := 0
			if value != "" {
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return req, fmt.Errorf("left %q must be a number of minutes", value)
				}
				minutes = parsed
			}
			req.RemainingMinutes = &minutes
		default:
			return req, fmt.Errorf("unknown field %q: use title, notes, status, difficulty, priority, due, start, waits_on, estimate, left, every or after", key)
		}
	}
	return req, nil
}

// printSaid puts what a completion changed under it, a line each.
func printSaid(w io.Writer, o *api.Outcome) {
	for _, line := range o.Said() {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func printTasks(w io.Writer, tasks []api.Task) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "no tasks")
		return
	}
	// The repeats column only earns its width when something in view repeats.
	repeats := false
	for _, t := range tasks {
		if t.Recur != nil {
			repeats = true
			break
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "ID\tDUE\tPRIORITY\tDIFFICULTY\t"
	if repeats {
		header += "REPEATS\t"
	}
	fmt.Fprintln(tw, header+"TITLE")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t", t.ID, dueCell(t), dash(t.Priority), dash(t.Difficulty))
		if repeats {
			fmt.Fprintf(tw, "%s\t", recurCell(t))
		}
		fmt.Fprintln(tw, titleCell(t))
	}
	tw.Flush()
}

// recurCell reads as the rule itself for a schedule, since "weekly on tue"
// already says what it means; an interval needs the word to disambiguate.
func recurCell(t api.Task) string {
	if t.Recur == nil {
		return "-"
	}
	if t.Recur.Kind == string(store.RecurAfter) {
		return "after " + t.Recur.Rule
	}
	if recur.Interval(t.Recur.Kind, t.Recur.Rule) {
		return "every " + t.Recur.Rule
	}
	return t.Recur.Rule
}

func dueSuffix(t *api.Task) string {
	if t.Due == "" {
		return ""
	}
	return " (due " + api.ShowDate(t.Due) + ")"
}

// dueCell shows the date the task has to be done by, which for a task others
// wait on can be earlier than its own, and says whose date it is.
func dueCell(t api.Task) string {
	date := t.Due
	if t.EffectiveDue != "" {
		date = t.EffectiveDue
	}
	if date == "" {
		return "-"
	}
	date = api.ShowDate(date)
	if t.Overdue {
		date += "!"
	}
	if t.EffectiveDue != "" {
		date += fmt.Sprintf(" for %d", t.DueFor)
	}
	return date
}

// titleCell marks a task the enrichment worker has not reached yet, so a bare
// row reads as pending rather than as deliberately empty.
func titleCell(t api.Task) string {
	title := t.Title
	if t.Status == string(store.StatusDone) {
		title = "[done] " + title
	}
	if t.DoneToday {
		title = "[done today] " + title
	}
	if !t.Enriched {
		title += " ~"
	}
	if wait := waitingNote(t); wait != "" {
		title += " (" + wait + ")"
	}
	if t.Mnemo != nil {
		title += " [[" + t.Mnemo.Slug + "]]"
		if t.Mnemo.Missing {
			title += " (missing)"
		}
	}
	return title
}

// waitingNote says why an open task cannot be started yet, or nothing.
func waitingNote(t api.Task) string {
	if t.Status != string(store.StatusOpen) {
		return ""
	}
	var ids []string
	for _, d := range t.BlockedBy {
		if !d.Done {
			ids = append(ids, strconv.FormatInt(d.ID, 10))
		}
	}
	switch {
	case len(ids) > 0:
		return "waits on " + strings.Join(ids, ", ")
	case t.Start > store.Today():
		return "from " + api.ShowDate(t.Start)
	}
	return ""
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
