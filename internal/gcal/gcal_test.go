package gcal

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

// fakeGoogle is enough of the Calendar API to see what the client sends and
// give it back: a token endpoint, and the four event calls.
type fakeGoogle struct {
	mu       sync.Mutex
	events   map[string]map[string]any
	next     int
	tokens   int
	pageSize int
	failWith int
	lastPath string
}

func newFakeGoogle() *fakeGoogle { return &fakeGoogle{events: map[string]map[string]any{}} }

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.URL.Path == "/token" {
		g.tokens++
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, `{"error":{"message":"Login Required"}}`, http.StatusUnauthorized)
		return
	}
	if g.failWith != 0 {
		http.Error(w, fmt.Sprintf(`{"error":{"code":%d,"message":"The caller does not have permission"}}`, g.failWith), g.failWith)
		return
	}
	g.lastPath = r.URL.Path
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// calendars/{id}/events[/{eventId}]
	switch {
	case len(parts) == 3 && r.Method == http.MethodGet:
		g.list(w, r)
	case len(parts) == 3 && r.Method == http.MethodPost:
		var e map[string]any
		json.NewDecoder(r.Body).Decode(&e)
		g.next++
		e["id"] = "ev" + strconv.Itoa(g.next)
		g.events[e["id"].(string)] = e
		json.NewEncoder(w).Encode(e)
	case len(parts) == 4 && r.Method == http.MethodPatch:
		e, ok := g.events[parts[3]]
		if !ok {
			http.Error(w, `{"error":{"code":404,"message":"Not Found"}}`, http.StatusNotFound)
			return
		}
		var patch map[string]any
		json.NewDecoder(r.Body).Decode(&patch)
		for k, v := range patch {
			e[k] = v
		}
		json.NewEncoder(w).Encode(e)
	case len(parts) == 4 && r.Method == http.MethodDelete:
		if _, ok := g.events[parts[3]]; !ok {
			http.Error(w, `{"error":{"code":404,"message":"Not Found"}}`, http.StatusNotFound)
			return
		}
		delete(g.events, parts[3])
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
	}
}

func (g *fakeGoogle) list(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, 0, len(g.events))
	for id := range g.events {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	start := 0
	if tok := r.URL.Query().Get("pageToken"); tok != "" {
		start, _ = strconv.Atoi(tok)
	}
	end := len(ids)
	next := ""
	if g.pageSize > 0 && start+g.pageSize < len(ids) {
		end = start + g.pageSize
		next = strconv.Itoa(end)
	}
	items := []map[string]any{}
	for _, id := range ids[start:end] {
		items = append(items, g.events[id])
	}
	json.NewEncoder(w).Encode(map[string]any{"items": items, "nextPageToken": next})
}

// keyFile writes a service account key with a real RSA key in it, because
// the JWT flow signs with it before anything reaches the fake.
func keyFile(t *testing.T, tokenURL string, mutate ...func(map[string]string)) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sa := map[string]string{
		"type":           "service_account",
		"client_email":   "ordo@example.iam.gserviceaccount.com",
		"private_key_id": "k1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      tokenURL,
	}
	for _, m := range mutate {
		m(sa)
	}
	raw, _ := json.Marshal(sa)
	path := filepath.Join(t.TempDir(), "google.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newClient(t *testing.T) (*Client, *fakeGoogle) {
	t.Helper()
	g := newFakeGoogle()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Options{
		ServiceAccount: keyFile(t, srv.URL+"/token"),
		Calendar:       "abc123@group.calendar.google.com",
		Base:           srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, g
}

func at(clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 "+clock, store.Location)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNewRefusesWhatIsNotAServiceAccountKey(t *testing.T) {
	_, err := New(context.Background(), Options{ServiceAccount: filepath.Join(t.TempDir(), "missing.json"), Calendar: "x"})
	if err == nil {
		t.Error("a missing key file was accepted")
	}
	user := keyFile(t, "", func(sa map[string]string) { sa["type"] = "authorized_user" })
	if _, err := New(context.Background(), Options{ServiceAccount: user, Calendar: "x"}); err == nil {
		t.Error("a user credential was accepted as a service account")
	}
	if _, err := New(context.Background(), Options{ServiceAccount: keyFile(t, ""), Calendar: ""}); err == nil {
		t.Error("an empty calendar id was accepted")
	}
}

func TestRoundTrip(t *testing.T) {
	c, g := newClient(t)
	ctx := context.Background()

	made, err := c.Insert(ctx, Event{Task: 14, Summary: "Rewrite the pricing model", Description: "deep work", Start: at("07:00"), End: at("08:30")})
	if err != nil {
		t.Fatal(err)
	}
	if made.ID == "" || made.Task != 14 {
		t.Fatalf("inserted = %+v", made)
	}
	if g.tokens != 1 {
		t.Errorf("token fetched %d times, want once", g.tokens)
	}

	got, err := c.List(ctx, at("00:00"), at("23:59"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Task != 14 || got[0].Summary != "Rewrite the pricing model" ||
		!got[0].Start.Equal(at("07:00")) || !got[0].End.Equal(at("08:30")) {
		t.Fatalf("listed = %+v", got)
	}

	made.Summary, made.Start, made.End = "Rewrite it later", at("19:00"), at("20:30")
	if _, err := c.Update(ctx, made); err != nil {
		t.Fatal(err)
	}
	got, _ = c.List(ctx, at("00:00"), at("23:59"))
	if len(got) != 1 || got[0].Summary != "Rewrite it later" || !got[0].Start.Equal(at("19:00")) {
		t.Fatalf("after update = %+v", got)
	}

	if err := c.Delete(ctx, made.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.List(ctx, at("00:00"), at("23:59")); len(got) != 0 {
		t.Fatalf("after delete = %+v", got)
	}
	// Deleting again is what a retried pass does, and it must not be a fault.
	if err := c.Delete(ctx, made.ID); err != nil {
		t.Fatalf("deleting a gone event: %v", err)
	}
	if !strings.HasPrefix(g.lastPath, "/calendars/abc123@group.calendar.google.com/events") {
		t.Errorf("requests went to %s, want the calendar's own path", g.lastPath)
	}
}

func TestListPagesThroughEverything(t *testing.T) {
	c, g := newClient(t)
	g.pageSize = 1
	for i := range 3 {
		if _, err := c.Insert(context.Background(), Event{Task: int64(i + 1), Summary: "x", Start: at("17:00"), End: at("17:15")}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.List(context.Background(), at("00:00"), at("23:59"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("listed %d events across pages, want 3", len(got))
	}
}

// An event a person added has no task property and comes back with Task
// zero; cancelled and all-day events are not blocks at all.
func TestListTellsOursFromTheirs(t *testing.T) {
	c, g := newClient(t)
	g.events["theirs"] = map[string]any{
		"id": "theirs", "summary": "Dentist",
		"start": map[string]any{"dateTime": at("14:00").Format(time.RFC3339)},
		"end":   map[string]any{"dateTime": at("14:30").Format(time.RFC3339)},
	}
	g.events["gone"] = map[string]any{
		"id": "gone", "status": "cancelled", "summary": "was ours",
		"start": map[string]any{"dateTime": at("15:00").Format(time.RFC3339)},
		"end":   map[string]any{"dateTime": at("15:30").Format(time.RFC3339)},
	}
	g.events["allday"] = map[string]any{
		"id": "allday", "summary": "Bank holiday",
		"start": map[string]any{"date": "2026-09-22"},
		"end":   map[string]any{"date": "2026-09-23"},
	}
	got, err := c.List(context.Background(), at("00:00"), at("23:59"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "theirs" || got[0].Task != 0 {
		t.Fatalf("listed = %+v, want only the hand-made event, unowned", got)
	}
}

func TestErrorsCarryGooglesMessage(t *testing.T) {
	c, g := newClient(t)
	g.failWith = http.StatusForbidden
	_, err := c.Insert(context.Background(), Event{Task: 1, Summary: "x", Start: at("17:00"), End: at("17:15")})
	if err == nil || !strings.Contains(err.Error(), "does not have permission") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want Google's own words and the status", err)
	}
	if err := c.Delete(context.Background(), "whatever"); err == nil {
		t.Error("a 403 on delete must not be mistaken for already gone")
	}
}

func TestWireCarriesTheTaskAndStaysTransparent(t *testing.T) {
	w := wire(Event{Task: 7, Summary: "x", Start: at("17:00"), End: at("17:15")})
	if w.Extended == nil || w.Extended.Private[taskProperty] != "7" {
		t.Errorf("task property = %+v", w.Extended)
	}
	if w.Transparency != "transparent" {
		t.Errorf("transparency = %q, want a block that does not make you look busy", w.Transparency)
	}
	if w.Start.TimeZone != "Europe/London" || w.Start.DateTime != at("17:00").Format(time.RFC3339) {
		t.Errorf("start = %+v", w.Start)
	}
}
