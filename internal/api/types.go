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

// Link is the note a task points at. Missing is set only when the vault has
// answered and the slug is not there; a vault that cannot be reached leaves
// it false, because "I could not ask" is not "it is gone".
type Link struct {
	Slug    string `json:"slug"`
	Title   string `json:"title,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

// Hit is a note the vault offered, from a search or a similarity lookup.
type Hit struct {
	Slug        string  `json:"slug"`
	Folder      string  `json:"folder,omitempty"`
	Description string  `json:"description,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

// RelatedResponse answers one question — what does the vault say about this
// task — in whichever of the three states the task is in. A linked task gets
// its note and its neighbourhood; an unlinked or orphaned one gets the
// candidates to link it to instead.
type RelatedResponse struct {
	Linked     bool     `json:"linked"`
	Note       *Note    `json:"note,omitempty"`
	Missing    bool     `json:"missing,omitempty"`
	Similar    []Hit    `json:"similar,omitempty"`
	Links      []string `json:"links,omitempty"`
	Backlinks  []string `json:"backlinks,omitempty"`
	Candidates []Hit    `json:"candidates,omitempty"`
}

type Note struct {
	Slug        string   `json:"slug"`
	Folder      string   `json:"folder,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Type        string   `json:"type,omitempty"`
}

type LinkRequest struct {
	Slug string `json:"slug"`
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
	MnemoSlug       string `json:"mnemo_slug,omitempty"`
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
