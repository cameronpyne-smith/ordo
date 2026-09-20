package api

// Task is the wire form. Empty optional fields are omitted rather than sent
// as null, so a bare task is a small object.
type Task struct {
	ID              int64  `json:"id"`
	Title           string `json:"title"`
	Notes           string `json:"notes,omitempty"`
	Status          string `json:"status"`
	Difficulty      string `json:"difficulty,omitempty"`
	Priority        string `json:"priority,omitempty"`
	EstimateMinutes int    `json:"estimate_minutes,omitempty"`
	Due             string `json:"due,omitempty"`
	Overdue         bool   `json:"overdue,omitempty"`
	Recur           *Recur `json:"recur,omitempty"`
	Mnemo           *Link  `json:"mnemo,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	DoneAt          string `json:"done_at,omitempty"`
	Enriched        bool   `json:"enriched"`
}

type Recur struct {
	Kind string `json:"kind"`
	Rule string `json:"rule"`
}

type Link struct {
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
}

type CreateRequest struct {
	Title           string `json:"title"`
	Notes           string `json:"notes,omitempty"`
	Difficulty      string `json:"difficulty,omitempty"`
	Priority        string `json:"priority,omitempty"`
	EstimateMinutes int    `json:"estimate_minutes,omitempty"`
	Due             string `json:"due,omitempty"`
	RecurKind       string `json:"recur_kind,omitempty"`
	RecurRule       string `json:"recur_rule,omitempty"`
}

// EditRequest changes only the fields it carries. An absent or null field is
// untouched; an empty string clears a text field and a zero estimate clears
// the estimate.
type EditRequest struct {
	Title           *string `json:"title,omitempty"`
	Notes           *string `json:"notes,omitempty"`
	Status          *string `json:"status,omitempty"`
	Difficulty      *string `json:"difficulty,omitempty"`
	Priority        *string `json:"priority,omitempty"`
	EstimateMinutes *int    `json:"estimate_minutes,omitempty"`
	Due             *string `json:"due,omitempty"`
	RecurKind       *string `json:"recur_kind,omitempty"`
	RecurRule       *string `json:"recur_rule,omitempty"`
}

type DoneRequest struct {
	Minutes int `json:"minutes,omitempty"`
}

type ListResponse struct {
	Tasks []Task `json:"tasks"`
	Count int    `json:"count"`
}

type DeleteResponse struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

type StatusResponse struct {
	Open       int    `json:"open"`
	Done       int    `json:"done"`
	Overdue    int    `json:"overdue"`
	Unenriched int    `json:"unenriched"`
	Today      string `json:"today"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
