package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/mcp"
	"github.com/cameronpyne-smith/ordo/internal/server"
	"github.com/cameronpyne-smith/ordo/internal/store"
	"github.com/cameronpyne-smith/ordo/internal/todo"
)

// A connected Claude session holds an MCP event stream open for as long as
// it lives. Shutting down has to end it rather than wait on it, or stopping
// the daemon takes the whole grace period and then fails.
func TestShutdownDoesNotWaitOnAnOpenMCPStream(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ordo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := todo.New(todo.Options{Store: st, Log: log})
	handler := server.New(server.Options{Todo: svc, MCP: mcp.Handler(svc, log), Log: log})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, ln, handler, 2*time.Second, log) }()

	base := "http://" + ln.Addr().String() + "/mcp"
	session := initialize(t, base)
	req, _ := http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event stream refused: %s", resp.Status)
	}

	started := time.Now()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown failed after %s: %v", time.Since(started), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown never returned")
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("shutdown took %s, want it prompt", took)
	}
}

func initialize(t *testing.T, url string) string {
	t.Helper()
	post := func(body, session string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	resp := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, "")
	session := resp.Header.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatalf("no session from initialize: %s", resp.Status)
	}
	post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, session)
	return session
}
