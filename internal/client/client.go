package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(base, token string) *Client {
	return &Client{base: base, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

type Filter struct {
	Status     string
	Difficulty string
	Priority   string
	Overdue    bool
	Linked     bool
	Note       string
	Recurring  bool
	Quick      bool
	Limit      int
}

func (f Filter) query() string {
	q := url.Values{}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.Difficulty != "" {
		q.Set("difficulty", f.Difficulty)
	}
	if f.Priority != "" {
		q.Set("priority", f.Priority)
	}
	if f.Overdue {
		q.Set("overdue", "true")
	}
	if f.Linked {
		q.Set("linked", "true")
	}
	if f.Note != "" {
		q.Set("note", f.Note)
	}
	if f.Recurring {
		q.Set("recurring", "true")
	}
	if f.Quick {
		q.Set("quick", "true")
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func (c *Client) List(f Filter) (*api.ListResponse, error) {
	var resp api.ListResponse
	return &resp, c.do(http.MethodGet, "/tasks"+f.query(), nil, &resp)
}

func (c *Client) Create(req api.CreateRequest) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks", req, &resp)
}

func (c *Client) Get(id int64) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodGet, "/tasks/"+strconv.FormatInt(id, 10), nil, &resp)
}

func (c *Client) Edit(id int64, req api.EditRequest) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/edit", req, &resp)
}

func (c *Client) Done(id int64, minutes int) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/done", api.DoneRequest{Minutes: minutes}, &resp)
}

func (c *Client) Undo(id int64) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/undo", nil, &resp)
}

func (c *Client) Enrich(id int64) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/enrich", nil, &resp)
}

func (c *Client) Link(id int64, slug string) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/link", api.LinkRequest{Slug: slug}, &resp)
}

func (c *Client) Unlink(id int64) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/unlink", nil, &resp)
}

func (c *Client) Related(id int64) (*api.RelatedResponse, error) {
	var resp api.RelatedResponse
	return &resp, c.do(http.MethodGet, "/tasks/"+strconv.FormatInt(id, 10)+"/related", nil, &resp)
}

func (c *Client) Delete(id int64) (*api.DeleteResponse, error) {
	var resp api.DeleteResponse
	return &resp, c.do(http.MethodDelete, "/tasks/"+strconv.FormatInt(id, 10), nil, &resp)
}

func (c *Client) Pin(id int64, day string) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/pin", api.PinRequest{Day: day}, &resp)
}

func (c *Client) Unpin(id int64) (*api.Task, error) {
	var resp api.Task
	return &resp, c.do(http.MethodPost, "/tasks/"+strconv.FormatInt(id, 10)+"/unpin", nil, &resp)
}

func (c *Client) Today(day string) (*api.TodayResponse, error) {
	path := "/today"
	if day != "" {
		path += "?day=" + url.QueryEscape(day)
	}
	var resp api.TodayResponse
	return &resp, c.do(http.MethodGet, path, nil, &resp)
}

func (c *Client) Preferences() (*api.Preferences, error) {
	var resp api.Preferences
	return &resp, c.do(http.MethodGet, "/preferences", nil, &resp)
}

func (c *Client) SetPreferences(req api.PreferencesRequest) (*api.Preferences, error) {
	var resp api.Preferences
	return &resp, c.do(http.MethodPost, "/preferences", req, &resp)
}

func (c *Client) Status() (*api.StatusResponse, error) {
	var resp api.StatusResponse
	return &resp, c.do(http.MethodGet, "/status", nil, &resp)
}

func (c *Client) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("contacting ordo at %s: %w", c.base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var failure api.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&failure); err == nil && failure.Error != "" {
			return errors.New(failure.Error)
		}
		return fmt.Errorf("ordo returned %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
