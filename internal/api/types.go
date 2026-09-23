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
	PinnedOn        string `json:"pinned_on,omitempty"`
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

type PinRequest struct {
	Day string `json:"day,omitempty"`
}

// Preferences is the shape of a day. Times are HH:MM, because one timezone
// and one user means a clock is all a time ever has to be here.
type Preferences struct {
	DayStart        string `json:"day_start"`
	DayEnd          string `json:"day_end"`
	DeepStart       string `json:"deep_start"`
	DeepEnd         string `json:"deep_end"`
	BufferMinutes   int    `json:"buffer_minutes"`
	MinBlockMinutes int    `json:"min_block_minutes"`
	MaxMinutesDay   int    `json:"max_minutes_per_day"`
}

// PreferencesRequest changes only what it carries, like EditRequest. The
// daemon reads the current row, applies these, and validates the whole day
// rather than one field of it.
type PreferencesRequest struct {
	DayStart        *string `json:"day_start,omitempty"`
	DayEnd          *string `json:"day_end,omitempty"`
	DeepStart       *string `json:"deep_start,omitempty"`
	DeepEnd         *string `json:"deep_end,omitempty"`
	BufferMinutes   *int    `json:"buffer_minutes,omitempty"`
	MinBlockMinutes *int    `json:"min_block_minutes,omitempty"`
	MaxMinutesDay   *int    `json:"max_minutes_per_day,omitempty"`
}

// Block is one task placed at a time, carrying why it landed there.
type Block struct {
	Task    Task   `json:"task"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Minutes int    `json:"minutes"`
	Reason  string `json:"reason"`
}

// Busy is time the calendar says is already taken.
type Busy struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Summary string `json:"summary,omitempty"`
}

type Window struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Minutes int    `json:"minutes"`
}

// Skip is a task that was a candidate and did not fit, with the reason.
type Skip struct {
	Task   Task   `json:"task"`
	Reason string `json:"reason"`
}

// TodayResponse is a whole day: what is planned, what the calendar took,
// what is left, and what did not make it. CalendarError is set when a feed
// is configured and could not be read, in which case the plan is still
// returned and simply knows less.
type TodayResponse struct {
	Date           string   `json:"date"`
	Blocks         []Block  `json:"blocks"`
	Busy           []Busy   `json:"busy,omitempty"`
	Free           []Window `json:"free,omitempty"`
	Skipped        []Skip   `json:"skipped,omitempty"`
	PlannedMinutes int      `json:"planned_minutes"`
	BudgetMinutes  int      `json:"budget_minutes"`
	Calendar       bool     `json:"calendar"`
	CalendarError  string   `json:"calendar_error,omitempty"`
}
