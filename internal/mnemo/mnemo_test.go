package mnemo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestVault(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "secret")
}

func TestGet(t *testing.T) {
	var auth, path string
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		w.Write([]byte(`{"slug":"career-transition","folder":"projects","description":"Quant move","body":"..."}`))
	})

	note, err := c.Get(context.Background(), "career-transition")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if note.Description != "Quant move" || note.Folder != "projects" {
		t.Fatalf("note = %+v, want the vault's answer", note)
	}
	if auth != "Bearer secret" {
		t.Fatalf("authorization = %q, want the bearer token", auth)
	}
	if path != "/notes/career-transition" {
		t.Fatalf("path = %q", path)
	}
}

func TestGetMissingIsItsOwnError(t *testing.T) {
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	})

	if _, err := c.Get(context.Background(), "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestExistsSeparatesMissingFromUnreachable(t *testing.T) {
	missing := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ok, err := missing.Exists(context.Background(), "gone")
	if err != nil || ok {
		t.Fatalf("exists = %v, %v; want false with no error", ok, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	down := New(srv.URL, "")
	if _, err := down.Exists(context.Background(), "anything"); err == nil {
		t.Fatal("a vault that cannot be reached must not answer 'missing'")
	}
}

func TestSearch(t *testing.T) {
	var query string
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`{"results":[{"slug":"latent","description":"The company","score":0.82}]}`))
	})

	hits, err := c.Search(context.Background(), "quant cv", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Slug != "latent" || hits[0].Score != 0.82 {
		t.Fatalf("hits = %+v", hits)
	}
	if query != "limit=3&q=quant+cv" {
		t.Fatalf("query = %q", query)
	}
}

func TestSimilar(t *testing.T) {
	var path, query string
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		w.Write([]byte(`{"results":[{"slug":"latent","description":"The company"}]}`))
	})

	hits, err := c.Similar(context.Background(), "career-transition", 5)
	if err != nil {
		t.Fatalf("similar: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	if path != "/notes/career-transition/similar" || query != "limit=5" {
		t.Fatalf("path = %q query = %q", path, query)
	}
}

func TestLinks(t *testing.T) {
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"slug":"a","links":["b","c"],"backlinks":["d"]}`))
	})

	links, backlinks, err := c.Links(context.Background(), "a")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	if len(links) != 2 || len(backlinks) != 1 {
		t.Fatalf("links = %v backlinks = %v", links, backlinks)
	}
}

func TestSlugsWithAwkwardCharactersAreEscaped(t *testing.T) {
	var path string
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"slug":"a b"}`))
	})

	if _, err := c.Get(context.Background(), "a b"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if path != "/notes/a b" {
		t.Fatalf("path = %q, want the slug escaped on the wire and decoded back", path)
	}
}

func TestVaultErrorsCarryTheirReason(t *testing.T) {
	c := newTestVault(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"embeddings are disabled"}`, http.StatusServiceUnavailable)
	})

	_, err := c.Similar(context.Background(), "a", 0)
	if errors.Is(err, ErrNotFound) {
		t.Fatal("a 503 is not a missing note")
	}
	if err == nil || err.Error() != "mnemo: embeddings are disabled" {
		t.Fatalf("err = %v, want the vault's reason", err)
	}
}
