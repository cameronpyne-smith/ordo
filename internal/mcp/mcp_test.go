package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/mnemo"
	"github.com/cameronpyne-smith/ordo/internal/store"
	"github.com/cameronpyne-smith/ordo/internal/todo"
)

type queueSpy struct {
	mu  sync.Mutex
	ids []int64
}

func (q *queueSpy) Queue(id int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
}

func (q *queueSpy) all() []int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]int64(nil), q.ids...)
}

func newSession(t *testing.T, vault *mnemo.Client) (*sdk.ClientSession, *queueSpy) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	spy := &queueSpy{}
	svc := todo.New(todo.Options{Store: st, Enrich: spy, Vault: vault})

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	if _, err := NewServer(svc).Connect(t.Context(), serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0.0.0"}, nil)
	sess, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, spy
}

// testVault serves the one note the link tests need, so they exercise the
// real mnemo client rather than a fake of it.
func testVault(t *testing.T) *mnemo.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search":
			w.Write([]byte(`{"results":[{"slug":"latent","description":"The company","score":0.8}]}`))
		case r.URL.Path == "/notes/latent":
			w.Write([]byte(`{"slug":"latent","folder":"projects","description":"The company"}`))
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return mnemo.New(srv.URL, "")
}

func call[T any](t *testing.T, sess *sdk.ClientSession, name string, args any) T {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, text(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: marshalling result: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: decoding into %T: %v", name, out, err)
	}
	return out
}

// callErr expects the tool to refuse, and returns why so a test can check
// the reason reached the model rather than being swallowed.
func callErr(t *testing.T, sess *sdk.ClientSession, name string, args any) string {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if !res.IsError {
		t.Fatalf("%s: wanted a refusal, got %+v", name, res.StructuredContent)
	}
	return text(res)
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsAreAllDescribed(t *testing.T) {
	sess, _ := newSession(t, nil)

	res, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if len(tool.Description) < 40 {
			t.Errorf("%s: description is too thin to be useful: %q", tool.Name, tool.Description)
		}
	}
	slices.Sort(names)
	want := []string{
		"todo_add", "todo_delete", "todo_done", "todo_link",
		"todo_list", "todo_related", "todo_set", "todo_undo",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestAddTakesTheSentenceAndQueuesIt(t *testing.T) {
	sess, spy := newSession(t, nil)

	task := call[api.Task](t, sess, "todo_add", AddArgs{Title: "Put the bins out"})
	if task.ID != 1 || task.Title != "Put the bins out" {
		t.Fatalf("task = %+v", task)
	}
	if task.Enriched {
		t.Fatal("a new task must not claim to be enriched before the model has seen it")
	}
	if got := spy.all(); !slices.Equal(got, []int64{1}) {
		t.Fatalf("queued %v, want the new task", got)
	}
}

func TestAddRefusesAValueOutsideTheEnum(t *testing.T) {
	sess, _ := newSession(t, nil)

	// The schema, not the store, has to catch this: the point of pinning the
	// enums is that a wrong value never becomes a call.
	msg := callErr(t, sess, "todo_add", map[string]any{"title": "Bins", "difficulty": "hard"})
	if !strings.Contains(msg, "difficulty") {
		t.Fatalf("refusal = %q, want it to name the field", msg)
	}
	list := call[api.ListResponse](t, sess, "todo_list", ListArgs{})
	if list.Count != 0 {
		t.Fatalf("count = %d, want the refused task not to exist", list.Count)
	}
}

func TestSetClearsAFieldWithAnEmptyValue(t *testing.T) {
	sess, _ := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Renew the passport", Priority: "high", Due: "2026-10-01"})
	empty := ""
	task := call[api.Task](t, sess, "todo_set", SetArgs{ID: 1, Due: &empty})
	if task.Due != "" {
		t.Fatalf("due = %q, want it cleared", task.Due)
	}
	if task.Priority != "high" {
		t.Fatalf("priority = %q, want the untouched field left alone", task.Priority)
	}
}

func TestRetitlingSendsItBackToTheModel(t *testing.T) {
	sess, spy := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Bins"})
	title := "Put the bins out every tuesday"
	call[api.Task](t, sess, "todo_set", SetArgs{ID: 1, Title: &title})
	if got := spy.all(); !slices.Equal(got, []int64{1, 1}) {
		t.Fatalf("queued %v, want the retitled task queued again", got)
	}

	notes := "green bin"
	call[api.Task](t, sess, "todo_set", SetArgs{ID: 1, Notes: &notes})
	if got := spy.all(); len(got) != 2 {
		t.Fatalf("queued %v, want an edit that is not a retitle to change nothing", got)
	}
}

func TestListIsFilteredAndOrdered(t *testing.T) {
	sess, _ := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Later", Due: "2026-12-01"})
	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Sooner", Due: "2026-10-01"})
	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Undated"})

	list := call[api.ListResponse](t, sess, "todo_list", ListArgs{})
	if list.Count != 3 {
		t.Fatalf("count = %d", list.Count)
	}
	if list.Tasks[0].Title != "Sooner" || list.Tasks[2].Title != "Undated" {
		t.Fatalf("order = %q, %q, %q", list.Tasks[0].Title, list.Tasks[1].Title, list.Tasks[2].Title)
	}

	call[api.Task](t, sess, "todo_done", DoneArgs{ID: 2})
	if open := call[api.ListResponse](t, sess, "todo_list", ListArgs{}); open.Count != 2 {
		t.Fatalf("open count = %d, want the completed one hidden", open.Count)
	}
	if all := call[api.ListResponse](t, sess, "todo_list", ListArgs{Status: "all"}); all.Count != 3 {
		t.Fatalf("all count = %d", all.Count)
	}
}

func TestDoneAndUndoOnARecurringTask(t *testing.T) {
	sess, _ := newSession(t, nil)

	created := call[api.Task](t, sess, "todo_add", AddArgs{
		Title: "Put the bins out", RecurKind: "every", RecurRule: "weekly on tue",
	})
	if created.Recur == nil || created.Due == "" {
		t.Fatalf("task = %+v, want a rule and a first date", created)
	}

	done := call[api.Task](t, sess, "todo_done", DoneArgs{ID: 1, Minutes: 5})
	if done.Status != "open" {
		t.Fatalf("status = %q, want a recurring task to stay open", done.Status)
	}
	if done.Due == created.Due {
		t.Fatalf("due = %q, want it advanced past %q", done.Due, created.Due)
	}

	back := call[api.Task](t, sess, "todo_undo", IDArgs{ID: 1})
	if back.Due != created.Due {
		t.Fatalf("due = %q, want %q restored", back.Due, created.Due)
	}
}

func TestLinkAndUnlinkAreOneTool(t *testing.T) {
	sess, _ := newSession(t, testVault(t))

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Ship the pricing model"})
	linked := call[api.Task](t, sess, "todo_link", LinkArgs{ID: 1, Slug: "latent"})
	if linked.Mnemo == nil || linked.Mnemo.Title != "The company" {
		t.Fatalf("link = %+v, want the note's description captured", linked.Mnemo)
	}

	if only := call[api.ListResponse](t, sess, "todo_list", ListArgs{Note: "latent"}); only.Count != 1 {
		t.Fatalf("count = %d, want the workstream view", only.Count)
	}

	cut := call[api.Task](t, sess, "todo_link", LinkArgs{ID: 1, Slug: ""})
	if cut.Mnemo != nil {
		t.Fatalf("link = %+v, want an empty slug to cut it", cut.Mnemo)
	}
}

func TestLinkRefusesANoteThatIsNotThere(t *testing.T) {
	sess, _ := newSession(t, testVault(t))

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Bins"})
	if msg := callErr(t, sess, "todo_link", LinkArgs{ID: 1, Slug: "invented"}); !strings.Contains(msg, "not found") {
		t.Fatalf("refusal = %q", msg)
	}
}

func TestRelatedOffersCandidatesForAnUnlinkedTask(t *testing.T) {
	sess, _ := newSession(t, testVault(t))

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Ship the pricing model"})
	related := call[api.RelatedResponse](t, sess, "todo_related", IDArgs{ID: 1})
	if related.Linked || len(related.Candidates) != 1 {
		t.Fatalf("related = %+v, want candidates", related)
	}

	call[api.Task](t, sess, "todo_link", LinkArgs{ID: 1, Slug: "latent"})
	linked := call[api.RelatedResponse](t, sess, "todo_related", IDArgs{ID: 1})
	if !linked.Linked || linked.Note == nil || linked.Note.Slug != "latent" {
		t.Fatalf("related = %+v, want the note itself", linked)
	}
}

func TestVaultToolsSayWhenTheVaultIsOff(t *testing.T) {
	sess, _ := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Bins"})
	if msg := callErr(t, sess, "todo_related", IDArgs{ID: 1}); !strings.Contains(msg, "mnemo is not configured") {
		t.Fatalf("refusal = %q, want it to say why", msg)
	}
	if msg := callErr(t, sess, "todo_link", LinkArgs{ID: 1, Slug: "latent"}); !strings.Contains(msg, "mnemo is not configured") {
		t.Fatalf("refusal = %q", msg)
	}
	// Cutting a link is ordo's own business and must survive the vault being
	// the broken thing.
	call[api.Task](t, sess, "todo_link", LinkArgs{ID: 1, Slug: ""})
}

func TestDeleteIsPermanent(t *testing.T) {
	sess, _ := newSession(t, nil)

	call[api.Task](t, sess, "todo_add", AddArgs{Title: "Mistake"})
	gone := call[api.DeleteResponse](t, sess, "todo_delete", IDArgs{ID: 1})
	if !gone.Deleted {
		t.Fatalf("delete = %+v", gone)
	}
	if msg := callErr(t, sess, "todo_delete", IDArgs{ID: 1}); !strings.Contains(msg, "not found") {
		t.Fatalf("refusal = %q", msg)
	}
}

func TestAMissingTaskIsAnErrorNotAPanic(t *testing.T) {
	sess, _ := newSession(t, nil)

	if msg := callErr(t, sess, "todo_set", SetArgs{ID: 99, Priority: ptr("high")}); !strings.Contains(msg, "not found") {
		t.Fatalf("refusal = %q", msg)
	}
	if msg := callErr(t, sess, "todo_done", DoneArgs{ID: 99}); !strings.Contains(msg, "not found") {
		t.Fatalf("refusal = %q", msg)
	}
}

func ptr(s string) *string { return &s }
