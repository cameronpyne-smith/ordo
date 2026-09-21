package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

func newLinkCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "link <id> [slug]",
		Short: "Point a task at a mnemo note, or show the notes it could point at",
		Long: "Point a task at a mnemo note. Without a slug, searches the vault for the\n" +
			"task's own words and prints what it found, which is also how a link is\n" +
			"repaired after the note has been renamed.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				related, err := c.Related(id)
				if err != nil {
					return err
				}
				printRelated(cmd.OutOrStdout(), id, related)
				return nil
			}
			t, err := c.Link(id, args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "linked %d: %s [[%s]]\n", t.ID, t.Title, t.Mnemo.Slug)
			return nil
		},
	}
}

func newUnlinkCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <id>",
		Short: "Cut a task's link to a note; the note is untouched",
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
			t, err := c.Unlink(id)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unlinked %d: %s\n", t.ID, t.Title)
			return nil
		},
	}
}

func newRelatedCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "related <id>",
		Short: "Show what the vault knows about a task's note",
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
			related, err := c.Related(id)
			if err != nil {
				return err
			}
			printRelated(cmd.OutOrStdout(), id, related)
			return nil
		},
	}
}

func printRelated(w io.Writer, id int64, r *api.RelatedResponse) {
	switch {
	case !r.Linked:
		fmt.Fprintf(w, "%d is not linked to a note\n", id)
		printCandidates(w, id, r.Candidates)
	case r.Missing:
		fmt.Fprintf(w, "the linked note is no longer in the vault; it may have been renamed or merged\n")
		printCandidates(w, id, r.Candidates)
	default:
		printNote(w, r)
	}
}

func printNote(w io.Writer, r *api.RelatedResponse) {
	fmt.Fprintf(w, "[[%s]]", r.Note.Slug)
	if r.Note.Folder != "" {
		fmt.Fprintf(w, "  %s", r.Note.Folder)
	}
	fmt.Fprintln(w)
	if r.Note.Description != "" {
		fmt.Fprintln(w, r.Note.Description)
	}
	if len(r.Similar) > 0 {
		fmt.Fprintln(w, "\nsimilar")
		printHits(w, r.Similar)
	}
	if len(r.Links) > 0 {
		fmt.Fprintf(w, "\nlinks      %s\n", strings.Join(r.Links, ", "))
	}
	if len(r.Backlinks) > 0 {
		fmt.Fprintf(w, "backlinks  %s\n", strings.Join(r.Backlinks, ", "))
	}
}

func printCandidates(w io.Writer, id int64, hits []api.Hit) {
	if len(hits) == 0 {
		fmt.Fprintln(w, "\nthe vault had nothing close to suggest")
		return
	}
	fmt.Fprintln(w, "\nclosest notes")
	printHits(w, hits)
	fmt.Fprintf(w, "\n  ordo link %d <slug>\n", id)
}

func printHits(w io.Writer, hits []api.Hit) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, h := range hits {
		fmt.Fprintf(tw, "  %s\t%s\n", h.Slug, h.Description)
	}
	tw.Flush()
}
