// Package ollama is a direct client for the box's ollama and nothing else.
// The daemon's model jobs are small, frequent, structured and private, so
// there is no provider abstraction here to configure, key or grow.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Timeout is generous because enrichment is a background job nobody is
// waiting on, and a large model on a home box is slow before it is broken.
const Timeout = 2 * time.Minute

type Client struct {
	url   string
	model string
	http  *http.Client
}

func New(url, model string) *Client {
	return &Client{
		url:   strings.TrimSuffix(url, "/"),
		model: model,
		http:  &http.Client{Timeout: Timeout},
	}
}

func (c *Client) Model() string { return c.model }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
	Format   any       `json:"format,omitempty"`
	Options  options   `json:"options"`
}

// Temperature is zero throughout: extraction from a fixed sentence should
// give the same answer every time it is asked.
type options struct {
	Temperature float64 `json:"temperature"`
}

type chatResponse struct {
	Message message `json:"message"`
	Error   string  `json:"error"`
}

// Chat asks for a single response shaped by a JSON schema and returns the
// model's content unparsed, leaving the caller to decide what a badly shaped
// answer means.
func (c *Client) Chat(ctx context.Context, system, prompt string, schema any) (json.RawMessage, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: prompt},
		},
		Stream:  false,
		Format:  schema,
		Options: options{Temperature: 0},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: contacting %s: %w", c.url, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var decoded chatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("ollama: decoding response: %w", err)
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("ollama: %s", decoded.Error)
	}
	content := strings.TrimSpace(decoded.Message.Content)
	if content == "" {
		return nil, fmt.Errorf("ollama: %s returned nothing", c.model)
	}
	return json.RawMessage(content), nil
}

type tagsResponse struct {
	Models []struct {
		Model string `json:"model"`
		Name  string `json:"name"`
	} `json:"models"`
}

// Check reports whether ollama is reachable and carries the configured model.
// The daemon runs without it either way; this exists so the reason nothing is
// being enriched appears in the log at startup rather than being hunted for.
func (c *Client) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("ollama: building request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: contacting %s: %w", c.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: %s", resp.Status)
	}
	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return fmt.Errorf("ollama: decoding models: %w", err)
	}
	var names []string
	for _, m := range tags.Models {
		name := m.Model
		if name == "" {
			name = m.Name
		}
		if sameModel(name, c.model) {
			return nil
		}
		names = append(names, name)
	}
	return fmt.Errorf("ollama at %s has no model %q; it has %s", c.url, c.model, strings.Join(names, ", "))
}

// sameModel treats a bare name as the latest tag, the way ollama's own CLI
// does, so a config without ":latest" still matches.
func sameModel(have, want string) bool {
	return withTag(have) == withTag(want)
}

func withTag(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return name + ":latest"
}
