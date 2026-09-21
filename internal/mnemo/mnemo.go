// Package mnemo reads the knowledge vault. It is read-only by construction:
// the client type has no method that writes, so the rule that ordo never
// touches mnemo is enforced by the code rather than remembered.
//
// A note has no title of its own. Its description is the human-readable line
// mnemo keeps about it, so that is what ordo stores and shows.
package mnemo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound means the vault answered, and the note is not there. It is
// distinct from an error reaching mnemo at all, because a rename looks like
// the first and a box reboot looks like the second, and ordo must not mark a
// link broken for the second.
var ErrNotFound = errors.New("note not found")

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(base, token string) *Client {
	return &Client{
		base:  strings.TrimSuffix(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

type Note struct {
	Slug        string   `json:"slug"`
	Folder      string   `json:"folder"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Type        string   `json:"type"`
	Body        string   `json:"body"`
	Links       []string `json:"links,omitempty"`
	Backlinks   []string `json:"backlinks,omitempty"`
}

type Hit struct {
	Slug        string  `json:"slug"`
	Folder      string  `json:"folder"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
}

type searchResponse struct {
	Results []Hit `json:"results"`
}

func (c *Client) Get(ctx context.Context, slug string) (*Note, error) {
	var note Note
	if err := c.get(ctx, "/notes/"+url.PathEscape(slug), &note); err != nil {
		return nil, err
	}
	return &note, nil
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var resp searchResponse
	if err := c.get(ctx, "/search?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func (c *Client) Similar(ctx context.Context, slug string, limit int) ([]Hit, error) {
	path := "/notes/" + url.PathEscape(slug) + "/similar"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var resp searchResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// Exists is the cheapest question ordo asks: is this slug still a note? A
// vault that cannot be reached answers neither yes nor no, so the error is
// returned rather than folded into false.
func (c *Client) Exists(ctx context.Context, slug string) (bool, error) {
	if _, err := c.Get(ctx, slug); errors.Is(err, ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

type errorResponse struct {
	Error string `json:"error"`
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("mnemo: building request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mnemo: contacting %s: %w", c.base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 400 {
		var failure errorResponse
		if err := json.NewDecoder(resp.Body).Decode(&failure); err == nil && failure.Error != "" {
			return fmt.Errorf("mnemo: %s", failure.Error)
		}
		return fmt.Errorf("mnemo returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("mnemo: decoding response: %w", err)
	}
	return nil
}
