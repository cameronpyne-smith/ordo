package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

type ListArgs struct {
	Status     string `json:"status,omitempty" jsonschema:"which tasks to list: open (the default), done, or all"`
	Difficulty string `json:"difficulty,omitempty" jsonschema:"only tasks of this difficulty"`
	Priority   string `json:"priority,omitempty" jsonschema:"only tasks of this priority"`
	Overdue    bool   `json:"overdue,omitempty" jsonschema:"only tasks whose due date has passed"`
	Linked     bool   `json:"linked,omitempty" jsonschema:"only tasks linked to a mnemo note"`
	Note       string `json:"note,omitempty" jsonschema:"only tasks linked to this note, by slug: the tasks in flight for one workstream"`
	Recurring  bool   `json:"recurring,omitempty" jsonschema:"only tasks that repeat"`
	Limit      int    `json:"limit,omitempty" jsonschema:"show at most this many"`
}

type AddArgs struct {
	Title           string `json:"title" jsonschema:"the task as a sentence, e.g. put the bins out every tuesday"`
	Notes           string `json:"notes,omitempty" jsonschema:"a free-text line of detail, not knowledge worth keeping in mnemo"`
	Difficulty      string `json:"difficulty,omitempty" jsonschema:"only if you actually know it; otherwise inference decides"`
	Priority        string `json:"priority,omitempty" jsonschema:"only if you actually know it; otherwise inference decides"`
	EstimateMinutes int    `json:"estimate_minutes,omitempty" jsonschema:"how long it will take, in minutes, if known"`
	Due             string `json:"due,omitempty" jsonschema:"due date as YYYY-MM-DD; only if the sentence does not already say it"`
	RecurKind       string `json:"recur_kind,omitempty" jsonschema:"every for a fixed cycle, after for an interval counted from each completion: every 2w is fortnightly, after 2w is two weeks from the day it was last done"`
	RecurRule       string `json:"recur_rule,omitempty" jsonschema:"for every: daily, weekly on tue, weekly on mon,thu, monthly on 1, monthly on last, yearly on 03-15, or an interval such as 3d, 2w, 1m. For after: 3d, 2w, 1m"`
	MnemoSlug       string `json:"mnemo_slug,omitempty" jsonschema:"slug of the mnemo note this task came out of; the note must already exist"`
}

type SetArgs struct {
	ID              int64   `json:"id" jsonschema:"the task id"`
	Title           *string `json:"title,omitempty" jsonschema:"a new title; this re-runs inference over the new sentence"`
	Notes           *string `json:"notes,omitempty" jsonschema:"replacement detail line, or empty to clear"`
	Status          *string `json:"status,omitempty" jsonschema:"open or done; prefer todo_done, which also handles recurrence"`
	Difficulty      *string `json:"difficulty,omitempty" jsonschema:"low, medium or high, or empty to clear"`
	Priority        *string `json:"priority,omitempty" jsonschema:"low, normal or high, or empty to clear"`
	EstimateMinutes *int    `json:"estimate_minutes,omitempty" jsonschema:"how long it will take in minutes; 0 clears it"`
	Due             *string `json:"due,omitempty" jsonschema:"YYYY-MM-DD, or empty to clear the due date"`
	RecurKind       *string `json:"recur_kind,omitempty" jsonschema:"every or after, or empty to stop it repeating"`
	RecurRule       *string `json:"recur_rule,omitempty" jsonschema:"the rule text for the kind"`
}

type IDArgs struct {
	ID int64 `json:"id" jsonschema:"the task id"`
}

type DoneArgs struct {
	ID      int64 `json:"id" jsonschema:"the task id"`
	Minutes int   `json:"minutes,omitempty" jsonschema:"how long it actually took, in minutes, if known"`
}

type LinkArgs struct {
	ID   int64  `json:"id" jsonschema:"the task id"`
	Slug string `json:"slug" jsonschema:"the mnemo note's slug, or empty to cut the link"`
}

func (t *toolServer) list(ctx context.Context, _ *sdk.CallToolRequest, args ListArgs) (*sdk.CallToolResult, api.ListResponse, error) {
	f := store.Filter{
		Status:     store.Status(args.Status),
		All:        args.Status == "all",
		Difficulty: store.Difficulty(args.Difficulty),
		Priority:   store.Priority(args.Priority),
		Overdue:    args.Overdue,
		Linked:     args.Linked,
		Note:       args.Note,
		Recurring:  args.Recurring,
		Limit:      args.Limit,
	}
	if f.All {
		f.Status = ""
	}
	resp, err := t.todo.List(ctx, f)
	return nil, resp, err
}

func (t *toolServer) add(ctx context.Context, _ *sdk.CallToolRequest, args AddArgs) (*sdk.CallToolResult, api.Task, error) {
	task, err := t.todo.Create(ctx, api.CreateRequest{
		Title:           args.Title,
		Notes:           args.Notes,
		Difficulty:      args.Difficulty,
		Priority:        args.Priority,
		EstimateMinutes: args.EstimateMinutes,
		Due:             args.Due,
		RecurKind:       args.RecurKind,
		RecurRule:       args.RecurRule,
		MnemoSlug:       args.MnemoSlug,
	})
	return nil, task, err
}

func (t *toolServer) set(_ context.Context, _ *sdk.CallToolRequest, args SetArgs) (*sdk.CallToolResult, api.Task, error) {
	task, err := t.todo.Edit(args.ID, api.EditRequest{
		Title:           args.Title,
		Notes:           args.Notes,
		Status:          args.Status,
		Difficulty:      args.Difficulty,
		Priority:        args.Priority,
		EstimateMinutes: args.EstimateMinutes,
		Due:             args.Due,
		RecurKind:       args.RecurKind,
		RecurRule:       args.RecurRule,
	})
	return nil, task, err
}

func (t *toolServer) done(_ context.Context, _ *sdk.CallToolRequest, args DoneArgs) (*sdk.CallToolResult, api.Task, error) {
	task, err := t.todo.Done(args.ID, args.Minutes)
	return nil, task, err
}

func (t *toolServer) undo(_ context.Context, _ *sdk.CallToolRequest, args IDArgs) (*sdk.CallToolResult, api.Task, error) {
	task, err := t.todo.Undo(args.ID)
	return nil, task, err
}

func (t *toolServer) remove(_ context.Context, _ *sdk.CallToolRequest, args IDArgs) (*sdk.CallToolResult, api.DeleteResponse, error) {
	if err := t.todo.Delete(args.ID); err != nil {
		return nil, api.DeleteResponse{}, err
	}
	return nil, api.DeleteResponse{ID: args.ID, Deleted: true}, nil
}

// link and unlink are one tool because from the model's side they are one
// decision — which note, if any, this task belongs to — and an empty value
// clearing a field is how the rest of ordo already reads.
func (t *toolServer) link(ctx context.Context, _ *sdk.CallToolRequest, args LinkArgs) (*sdk.CallToolResult, api.Task, error) {
	if args.Slug == "" {
		task, err := t.todo.Unlink(args.ID)
		return nil, task, err
	}
	task, err := t.todo.Link(ctx, args.ID, args.Slug)
	return nil, task, err
}

func (t *toolServer) related(ctx context.Context, _ *sdk.CallToolRequest, args IDArgs) (*sdk.CallToolResult, api.RelatedResponse, error) {
	resp, err := t.todo.Related(ctx, args.ID)
	return nil, resp, err
}
