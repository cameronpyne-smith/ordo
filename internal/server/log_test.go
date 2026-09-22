package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
	"github.com/cameronpyne-smith/ordo/internal/todo"
)

func newLoggingServer(t *testing.T) (http.Handler, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var log bytes.Buffer
	svc := todo.New(todo.Options{Store: st, Enrich: &queueSpy{}})
	h := New(Options{
		Todo:  svc,
		Token: testToken,
		Log:   slog.New(slog.NewTextHandler(&log, nil)),
	})
	return h, &log
}

func TestEveryRequestIsLogged(t *testing.T) {
	h, log := newLoggingServer(t)

	if rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Move sofa into office"}); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	line := log.String()
	for _, want := range []string{"method=POST", "path=/tasks", "status=201", "ms="} {
		if !strings.Contains(line, want) {
			t.Errorf("log is missing %q\n%s", want, line)
		}
	}
}

// A request turned away for a bad token is the one you most need to see:
// something somewhere else is being refused and saying nothing about it.
func TestARefusedRequestIsLogged(t *testing.T) {
	h, log := newLoggingServer(t)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if line := log.String(); !strings.Contains(line, "status=401") {
		t.Errorf("a refused request was not logged\n%s", line)
	}
}

func TestTheLoggerIsOptional(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := New(Options{Todo: todo.New(todo.Options{Store: st}), Token: testToken})

	if rec := request(t, h, http.MethodGet, "/status", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestClientHostDropsThePort(t *testing.T) {
	if got := clientHost("100.103.58.27:52344"); got != "100.103.58.27" {
		t.Errorf("clientHost = %q", got)
	}
	if got := clientHost("pipe"); got != "pipe" {
		t.Errorf("an address with no port should survive, got %q", got)
	}
}
