package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

func newTagCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tag <id> <tag>...",
		Short: "Add tags to a task; a tag is one word, like house or career",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return retag(cmd, *configPath, args[0], args[1:], func(have, given []string) []string {
				return append(have, given...)
			})
		},
	}
}

func newUntagCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "untag <id> <tag>...",
		Short: "Take tags off a task",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return retag(cmd, *configPath, args[0], args[1:], func(have, given []string) []string {
				return slices.DeleteFunc(have, func(tag string) bool { return slices.Contains(given, tag) })
			})
		},
	}
}

// retag reads the task's tags, changes them, and sends the whole set back,
// since the API replaces tags the way it replaces what a task waits on.
func retag(cmd *cobra.Command, configPath, rawID string, given []string, change func(have, given []string) []string) error {
	id, err := parseID(rawID)
	if err != nil {
		return err
	}
	for i, tag := range given {
		if given[i], err = store.NormaliseTag(tag); err != nil {
			return err
		}
	}
	c, err := newClient(configPath)
	if err != nil {
		return err
	}
	t, err := c.Get(id)
	if err != nil {
		return err
	}
	tags := change(slices.Clone(t.Tags), given)
	t, err = c.Edit(id, api.EditRequest{Tags: &tags})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%d: %s\n", t.ID, titleCell(*t))
	return nil
}

func newTagsCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tags",
		Short: "The tags in use, with how many tasks carry each",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(*configPath)
			if err != nil {
				return err
			}
			resp, err := c.Tags()
			if err != nil {
				return err
			}
			printTags(cmd.OutOrStdout(), resp.Tags)
			return nil
		},
	}
}

func printTags(w io.Writer, tags []api.TagCount) {
	if len(tags) == 0 {
		fmt.Fprintln(w, "no tags in use")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TAG\tOPEN\tDONE")
	for _, t := range tags {
		fmt.Fprintf(tw, "#%s\t%d\t%d\n", t.Tag, t.Open, t.Done)
	}
	tw.Flush()
}

// badges is a task's tags as they read in front of its title: #house #reno.
func badges(t api.Task) string {
	if len(t.Tags) == 0 {
		return ""
	}
	return "#" + strings.Join(t.Tags, " #") + " "
}

// readTags is a list of tags as typed on one line: commas, spaces or both.
func readTags(raw string) ([]string, error) {
	tags := []string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		tag, err := store.NormaliseTag(part)
		if err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, nil
}
