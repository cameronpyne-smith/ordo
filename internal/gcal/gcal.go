// Package gcal writes to one Google calendar as a service account. It is the
// narrowest client that will do: list a window, insert, patch, delete. The
// calendar it is given is ordo's own, and the only events it ever touches
// are the ones it made, which it recognises by a private property rather
// than by anything a person could set from a phone.
package gcal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"golang.org/x/oauth2/jwt"

	"github.com/cameronpyne-smith/ordo/internal/store"
)

const (
	DefaultBase = "https://www.googleapis.com/calendar/v3"

	// scope is events only. The client never creates or reshapes a
	// calendar, so it is never given the right to.
	scope = "https://www.googleapis.com/auth/calendar.events"

	// taskProperty is how an event says which task it is a block for. It is
	// stored as a private extended property, invisible in every calendar
	// app, so an event a person adds by hand can never look like ours.
	taskProperty = "ordo_task"

	requestTimeout = 20 * time.Second
	pageSize       = 250
)

// Event is a block on the calendar. Task is zero for an event ordo did not
// make, which the publisher uses to leave it alone.
type Event struct {
	ID          string
	Task        int64
	Summary     string
	Description string
	Start, End  time.Time
}

type Options struct {
	// ServiceAccount is the path to the JSON key Google issued for the
	// service account the calendar has been shared with.
	ServiceAccount string
	// Calendar is the id of the calendar to write, as its settings page
	// shows it: an address ending in group.calendar.google.com.
	Calendar string

	// Base and TokenURL exist for the tests, which stand in for Google.
	Base     string
	TokenURL string
}

type Client struct {
	http     *http.Client
	base     string
	calendar string
}

// serviceAccount is the part of Google's key file the JWT flow needs.
type serviceAccount struct {
	Type         string `json:"type"`
	Email        string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
}

// New reads the key file and prepares a client whose requests carry a token
// it fetches and renews itself. Nothing is contacted until the first call.
func New(ctx context.Context, o Options) (*Client, error) {
	if o.Calendar == "" {
		return nil, errors.New("gcal: no calendar id")
	}
	raw, err := os.ReadFile(o.ServiceAccount)
	if err != nil {
		return nil, fmt.Errorf("gcal: reading service account key: %w", err)
	}
	var sa serviceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("gcal: service account key %s: %w", o.ServiceAccount, err)
	}
	if sa.Type != "service_account" || sa.Email == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("gcal: %s is not a service account key", o.ServiceAccount)
	}
	tokenURL := o.TokenURL
	if tokenURL == "" {
		tokenURL = sa.TokenURI
	}
	base := o.Base
	if base == "" {
		base = DefaultBase
	}
	cfg := jwt.Config{
		Email:        sa.Email,
		PrivateKey:   []byte(sa.PrivateKey),
		PrivateKeyID: sa.PrivateKeyID,
		Scopes:       []string{scope},
		TokenURL:     tokenURL,
	}
	client := cfg.Client(ctx)
	client.Timeout = requestTimeout
	return &Client{http: client, base: base, calendar: o.Calendar}, nil
}

// List returns the events in [from, to), all of them, so the publisher can
// tell ours from anything a person put there.
func (c *Client) List(ctx context.Context, from, to time.Time) ([]Event, error) {
	var out []Event
	page := ""
	for {
		q := url.Values{}
		q.Set("timeMin", from.Format(time.RFC3339))
		q.Set("timeMax", to.Format(time.RFC3339))
		q.Set("singleEvents", "true")
		q.Set("maxResults", strconv.Itoa(pageSize))
		if page != "" {
			q.Set("pageToken", page)
		}
		var resp struct {
			Items    []wireEvent `json:"items"`
			NextPage string      `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodGet, c.events()+"?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		for _, w := range resp.Items {
			if e, ok := w.event(); ok {
				out = append(out, e)
			}
		}
		if resp.NextPage == "" {
			return out, nil
		}
		page = resp.NextPage
	}
}

func (c *Client) Insert(ctx context.Context, e Event) (Event, error) {
	var created wireEvent
	if err := c.do(ctx, http.MethodPost, c.events(), wire(e), &created); err != nil {
		return Event{}, err
	}
	out, _ := created.event()
	return out, nil
}

// Update rewrites the fields ordo owns on an event it made. A patch rather
// than a put, so anything a calendar app added to the event survives.
func (c *Client) Update(ctx context.Context, e Event) (Event, error) {
	if e.ID == "" {
		return Event{}, errors.New("gcal: update needs an event id")
	}
	var updated wireEvent
	if err := c.do(ctx, http.MethodPatch, c.events()+"/"+url.PathEscape(e.ID), wire(e), &updated); err != nil {
		return Event{}, err
	}
	out, _ := updated.event()
	return out, nil
}

// Delete removes an event, and is content when it is already gone.
func (c *Client) Delete(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, c.events()+"/"+url.PathEscape(id), nil, nil)
	var missing *apiError
	if errors.As(err, &missing) && (missing.code == http.StatusNotFound || missing.code == http.StatusGone) {
		return nil
	}
	return err
}

func (c *Client) events() string {
	return c.base + "/calendars/" + url.PathEscape(c.calendar) + "/events"
}

func (c *Client) do(ctx context.Context, method, target string, body, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gcal: encoding %s: %w", method, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("gcal: %s: %w", method, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gcal: %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return readError(method, req.URL.Path, resp)
	}
	if into == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("gcal: %s %s: decoding: %w", method, req.URL.Path, err)
	}
	return nil
}

// apiError keeps the status so Delete can tell "already gone" from "broken",
// and carries Google's own message, which is the readable part.
type apiError struct {
	code    int
	method  string
	path    string
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gcal: %s %s: %d %s", e.method, e.path, e.code, e.message)
}

func readError(method, path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var google struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := string(bytes.TrimSpace(body))
	if json.Unmarshal(body, &google) == nil && google.Error.Message != "" {
		message = google.Error.Message
	}
	return &apiError{code: resp.StatusCode, method: method, path: path, message: message}
}

// The wire shapes are Google's, kept to the handful of fields ordo uses.

type wireEvent struct {
	ID           string        `json:"id,omitempty"`
	Status       string        `json:"status,omitempty"`
	Summary      string        `json:"summary"`
	Description  string        `json:"description,omitempty"`
	Start        wireTime      `json:"start"`
	End          wireTime      `json:"end"`
	Extended     *wireExtended `json:"extendedProperties,omitempty"`
	Transparency string        `json:"transparency,omitempty"`
}

type wireTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type wireExtended struct {
	Private map[string]string `json:"private,omitempty"`
}

// wire shapes an event for sending. Blocks are transparent: a plan is an
// intention, not a commitment to someone else, so it must not make you look
// busy to anyone who can see this calendar.
func wire(e Event) wireEvent {
	zone := store.Location.String()
	return wireEvent{
		Summary:     e.Summary,
		Description: e.Description,
		Start:       wireTime{DateTime: e.Start.In(store.Location).Format(time.RFC3339), TimeZone: zone},
		End:         wireTime{DateTime: e.End.In(store.Location).Format(time.RFC3339), TimeZone: zone},
		Extended: &wireExtended{Private: map[string]string{
			taskProperty: strconv.FormatInt(e.Task, 10),
		}},
		Transparency: "transparent",
	}
}

// event reads an event back. Cancelled and all-day events are not ours and
// are dropped; an event with no task property is someone else's and is kept
// with Task zero so the publisher knows not to touch it.
func (w wireEvent) event() (Event, bool) {
	if w.Status == "cancelled" || w.Start.DateTime == "" || w.End.DateTime == "" {
		return Event{}, false
	}
	start, err := time.Parse(time.RFC3339, w.Start.DateTime)
	if err != nil {
		return Event{}, false
	}
	end, err := time.Parse(time.RFC3339, w.End.DateTime)
	if err != nil {
		return Event{}, false
	}
	e := Event{ID: w.ID, Summary: w.Summary, Description: w.Description, Start: start, End: end}
	if w.Extended != nil {
		e.Task, _ = strconv.ParseInt(w.Extended.Private[taskProperty], 10, 64)
	}
	return e, true
}
