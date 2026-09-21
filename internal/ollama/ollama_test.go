package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatSendsASchemaAndReturnsTheContent(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s, want /api/chat", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Write([]byte(`{"message":{"role":"assistant","content":"{\"difficulty\":\"low\"}"}}`))
	}))
	defer srv.Close()

	schema := map[string]any{"type": "object"}
	content, err := New(srv.URL+"/", "test-model").Chat(context.Background(), "be terse", "bins", schema)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if string(content) != `{"difficulty":"low"}` {
		t.Fatalf("content = %s, want the message content", content)
	}
	if got.Model != "test-model" || got.Stream || got.Format == nil {
		t.Fatalf("request = %+v, want a non-streaming schema call", got)
	}
	if len(got.Messages) != 2 || got.Messages[1].Content != "bins" {
		t.Fatalf("messages = %+v, want system then prompt", got.Messages)
	}
}

func TestChatReportsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "missing").Chat(context.Background(), "", "bins", nil)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err = %v, want the server's reason", err)
	}
}

func TestChatRejectsAnEmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"role":"assistant","content":"  "}}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "quiet").Chat(context.Background(), "", "bins", nil); err == nil {
		t.Fatal("an empty answer should be an error, not empty JSON")
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"model":"qwen3:8b"},{"model":"llama3.2:latest"}]}`))
	}))
	defer srv.Close()

	if err := New(srv.URL, "qwen3:8b").Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := New(srv.URL, "llama3.2").Check(context.Background()); err != nil {
		t.Fatalf("a bare name should match the latest tag: %v", err)
	}
	err := New(srv.URL, "absent").Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "qwen3:8b") {
		t.Fatalf("err = %v, want the available models named", err)
	}
}

func TestCheckReportsAnUnreachableOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	if err := New(srv.URL, "any").Check(context.Background()); err == nil {
		t.Fatal("a closed server should be an error")
	}
}
