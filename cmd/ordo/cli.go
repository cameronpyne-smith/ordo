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
			return nil
		},
	}
	cmd.Flags().BoolVar(&done, "done", false, "list completed tasks instead of open ones")
	cmd.Flags().BoolVar(&all, "all", false, "list every task regardless of status")
	cmd.Flags().BoolVar(&f.Overdue, "overdue", false, "only tasks past their due date")
	cmd.Flags().BoolVar(&f.Linked, "linked", false, "only tasks linked to a mnemo note")
	cmd.Flags().StringVar(&f.Difficulty, "difficulty", "", "low, medium or high")
	cmd.Flags().StringVar(&f.Priority, "priority", "", "low, normal or high")
	cmd.Flags().IntVar(&f.Limit, "limit", 0, "show at most this many")
	return cmd
}

func newAddCmd(configPath *string) *cobra.Command {
	var req api.CreateRequest

	cmd := &cobra.Command{
		Use:   "add <title...>",
		Short: "Add a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			req.Title = strings.Join(args, " ")
			t, err := c.Create(req)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %d: %s\n", t.ID, t.Title)
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Notes, "notes", "", "a one-liner worth keeping with the task")
	cmd.Flags().StringVar(&req.Due, "due", "", "due date, YYYY-MM-DD")
	cmd.Flags().StringVar(&req.Difficulty, "difficulty", "", "low, medium or high")
	cmd.Flags().StringVar(&req.Priority, "priority", "", "low, normal or high")
	cmd.Flags().IntVar(&req.EstimateMinutes, "estimate", 0, "estimated minutes")
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
			fmt.Fprintf(cmd.OutOrStdout(), "done %d: %s\n", t.ID, t.Title)
			return nil
		},
	}
	cmd.Flags().IntVar(&minutes, "minutes", 0, "how long it actually took")
	return cmd
}

func newSetCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "set <id> <key=value>...",
		Short: "Change fields on a task; an empty value clears one",
		Long: "Change fields on a task. Keys: title, notes, status, difficulty, priority, due, estimate.\n" +
			"An empty value clears the field, for example due=.",
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
		Short: "Remove the most recent completion and reopen a task",
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
			fmt.Fprintf(cmd.OutOrStdout(), "reopened %d: %s\n", t.ID, t.Title)
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
			req.Due = &value
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
		default:
			return req, fmt.Errorf("unknown field %q: use title, notes, status, difficulty, priority, due or estimate", key)
		}
	}
	return req, nil
}

func printTasks(w io.Writer, tasks []api.Task) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "no tasks")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDUE\tPRIORITY\tDIFFICULTY\tTITLE")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			t.ID, dueCell(t), dash(t.Priority), dash(t.Difficulty), titleCell(t))
	}
	tw.Flush()
}

func dueCell(t api.Task) string {
	if t.Due == "" {
		return "-"
	}
	if t.Overdue {
		return t.Due + "!"
	}
	return t.Due
}

// titleCell marks a task the enrichment worker has not reached yet, so a bare
// row reads as pending rather than as deliberately empty.
func titleCell(t api.Task) string {
	title := t.Title
	if t.Status == string(store.StatusDone) {
		title = "[done] " + title
	}
	if !t.Enriched {
		title += " ~"
	}
	if t.Mnemo != nil {
		title += " [[" + t.Mnemo.Slug + "]]"
	}
	return title
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
