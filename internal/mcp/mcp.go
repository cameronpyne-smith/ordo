// Package mcp is the model-facing surface: the same operations as the HTTP
// API, described so a Claude session can use them without being told how
// ordo works. It is mounted on the daemon's own listener at /mcp, behind the
// same bearer token.
package mcp

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cameronpyne-smith/ordo/internal/todo"
)

// The enums the daemon already enforces on write, restated in the tool
// schemas so a wrong value is refused before the call is made rather than
// coming back as a 400. The empty string is a legal value wherever clearing
// a field is legal.
var (
	difficulties = []any{"low", "medium", "high"}
	priorities   = []any{"low", "normal", "high"}
	recurKinds   = []any{"every", "after"}
	statuses     = []any{"open", "done"}
)

type toolServer struct {
	todo *todo.Service
}

func NewServer(svc *todo.Service) *sdk.Server {
	t := &toolServer{todo: svc}
	srv := sdk.NewServer(&sdk.Implementation{Name: "ordo", Version: "0.1.0"}, nil)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_list",
		Description: "List tasks in ordo's order: overdue first, then by due date with undated last, " +
			"then priority, then difficulty. Open tasks only unless you say otherwise. This is the " +
			"whole list — read it before answering anything about what to do next, and re-read it " +
			"rather than remembering it, because the daemon fills fields in asynchronously.",
		InputSchema: schemaFor[ListArgs](map[string][]any{
			"status":     {"open", "done", "all"},
			"difficulty": difficulties,
			"priority":   priorities,
		}),
	}, t.list)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_add",
		Description: "Add a task. Put the whole sentence in the title and leave the rest alone: a local " +
			"model reads it in the background and fills in due date, difficulty, recurrence and " +
			"priority by itself, so \"put the bins out every tuesday\" becomes a weekly recurring task " +
			"without you parsing it. Set a field explicitly only when you know something the sentence " +
			"does not say — whatever you set here is never overwritten by that inference. Returns " +
			"immediately, before inference has run.",
		InputSchema: schemaFor[AddArgs](map[string][]any{
			"difficulty": difficulties,
			"priority":   priorities,
			"recur_kind": recurKinds,
		}),
	}, t.add)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_done",
		Description: "Complete a task. A recurring task stays open and its due date advances to the next " +
			"occurrence; a one-off becomes done. Pass minutes when you know how long it really took, " +
			"which is what calibrates future estimates.",
	}, t.done)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_undo",
		Description: "Remove a task's most recent completion: the fix for something ticked off by mistake. " +
			"A one-off reopens; a recurring task's previous due date comes back.",
	}, t.undo)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_set",
		Description: "Change fields on a task. Only the fields you pass change, and an empty value clears " +
			"one. Editing priority is expected and welcome: inference only ever fills fields that are " +
			"empty, so it will not quietly undo you. A new title re-runs inference over the new " +
			"sentence, which fills anything still empty — to have it reconsider a field it already " +
			"wrote, clear that field here first.",
		InputSchema: schemaFor[SetArgs](map[string][]any{
			"status":     append(statuses, nil),
			"difficulty": append(append([]any{""}, difficulties...), nil),
			"priority":   append(append([]any{""}, priorities...), nil),
			"recur_kind": append(append([]any{""}, recurKinds...), nil),
		}),
	}, t.set)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_link",
		Description: "Point a task at a mnemo note by slug, or pass an empty slug to cut the link. The note " +
			"must already exist: ordo reads mnemo and never writes to it, so linking copies nothing " +
			"and there is nothing to keep in sync. Use it when a task came out of something in the " +
			"vault — the note holds the thinking, the tasks are what is in flight from it, and " +
			"todo_list with note set then shows that one workstream.",
	}, t.link)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_related",
		Description: "What mnemo knows about a task. For a linked task: the note, its wikilinks and " +
			"backlinks, and the notes semantically nearest to it. For an unlinked task, or one whose " +
			"note has been renamed away, candidate notes to link it to instead.",
	}, t.related)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "todo_delete",
		Description: "Delete a task and its completion history permanently. There is no trash and no undo. " +
			"Finishing something is todo_done; use this only for a task that should never have existed.",
	}, t.remove)

	return srv
}

func Handler(svc *todo.Service, log *slog.Logger) http.Handler {
	srv := NewServer(svc)
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Logger: log},
	)
}

// schemaFor infers a tool's schema from its argument struct and then pins the
// enums on, which the struct tags cannot express. A name that is not a field
// panics at startup rather than shipping a schema that silently allows
// anything.
func schemaFor[T any](enums map[string][]any) *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp: inferring schema for %T: %v", *new(T), err))
	}
	for field, values := range enums {
		prop, ok := s.Properties[field]
		if !ok {
			panic(fmt.Sprintf("mcp: %T has no field %q to constrain", *new(T), field))
		}
		prop.Enum = values
	}
	return s
}
