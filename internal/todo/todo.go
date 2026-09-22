// Package todo is what ordo does, independent of how it is asked. Every
// surface — the HTTP API, the MCP tools, and through the API the CLI and the
// TUI — goes through this one type, so a task added by Claude gets exactly
// what a task typed at a prompt gets: the same validation, the same
// enrichment, the same link checking.
package todo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/calendar"
	"github.com/cameronpyne-smith/ordo/internal/mnemo"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// relatedLimit is how many notes the vault is asked for. A list you have to
// scroll is not a suggestion.
const relatedLimit = 5

// orphanCheckTimeout bounds the extra work a linked listing does. Past it the
// links are simply not annotated, because a slow vault must not turn into a
// slow todo list.
const orphanCheckTimeout = 3 * time.Second

// orphanCheckWorkers keeps a long linked list from opening one connection per
// task while still not asking for them one at a time.
const orphanCheckWorkers = 8

// ErrNoVault and ErrNoModel are the two optional collaborators saying they
// are not there. Both are states of the daemon rather than faults, so they
// are sentinels a surface can recognise and phrase in its own way.
var (
	ErrNoVault = errors.New("mnemo is not configured: set [mnemo] url and token on the daemon")
	ErrNoModel = errors.New("enrichment is off: set [ollama] url and model on the daemon")
)

// Enqueuer is how a task is handed to the model. It is an interface, and may
// be nil, so the daemon runs the same with enrichment switched off as on.
type Enqueuer interface {
	Queue(id int64)
}

// Nudger is told that the plan may have changed. Like Enqueuer it may be
// nil: publishing the day somewhere is optional, and the service does not
// know or care where.
type Nudger interface {
	Nudge()
}

type Options struct {
	Store    *store.Store
	Enrich   Enqueuer
	Vault    *mnemo.Client
	Calendar *calendar.Client
	Publish  Nudger
	Log      *slog.Logger
}

type Service struct {
	store    *store.Store
	enrich   Enqueuer
	vault    *mnemo.Client
	calendar *calendar.Client
	publish  Nudger
	log      *slog.Logger
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		store:    opts.Store,
		enrich:   opts.Enrich,
		vault:    opts.Vault,
		calendar: opts.Calendar,
		publish:  opts.Publish,
		log:      log,
	}
}

// Linked reports whether links can be resolved at all, so a surface can say
// so up front rather than offering something that will fail.
func (s *Service) Linked() bool { return s.vault != nil }

func (s *Service) List(ctx context.Context, f store.Filter) (api.ListResponse, error) {
	tasks, err := s.store.List(f)
	if err != nil {
		return api.ListResponse{}, err
	}
	out := api.FromTasks(tasks)
	// The linked views are the ones whose whole point is the links, so they
	// are the ones that pay to check them. A plain list stays one read.
	if f.Linked || f.Note != "" {
		s.markOrphans(ctx, out)
	}
	return api.ListResponse{Tasks: out, Count: len(out)}, nil
}

func (s *Service) Get(id int64) (api.Task, error) {
	t, err := s.store.Get(id)
	if err != nil {
		return api.Task{}, err
	}
	return api.FromTask(t), nil
}

func (s *Service) Create(ctx context.Context, req api.CreateRequest) (api.Task, error) {
	slug, title, err := s.resolveLink(ctx, req.MnemoSlug)
	if err != nil {
		return api.Task{}, err
	}
	t, err := s.store.Create(&store.Task{
		Title:           req.Title,
		Notes:           req.Notes,
		Difficulty:      store.Difficulty(req.Difficulty),
		Priority:        store.Priority(req.Priority),
		EstimateMinutes: req.EstimateMinutes,
		Due:             req.Due,
		RecurKind:       store.RecurKind(req.RecurKind),
		RecurRule:       req.RecurRule,
		MnemoSlug:       slug,
		MnemoTitle:      title,
	})
	if err != nil {
		return api.Task{}, err
	}
	s.queue(t.ID)
	s.changed()
	return api.FromTask(t), nil
}

// Edit re-queues enrichment when the title changed, which is the only edit
// that can alter what a task means.
func (s *Service) Edit(id int64, req api.EditRequest) (api.Task, error) {
	before, err := s.store.Get(id)
	if err != nil {
		return api.Task{}, err
	}
	edit := req.Edit()
	t, err := s.store.Edit(id, edit)
	if err != nil {
		return api.Task{}, err
	}
	if edit.TitleChanged(before.Title) {
		s.queue(t.ID)
	}
	s.changed()
	return api.FromTask(t), nil
}

func (s *Service) Done(id int64, minutes int) (api.Task, error) {
	t, err := s.store.Done(id, minutes)
	if err != nil {
		return api.Task{}, err
	}
	s.changed()
	return api.FromTask(t), nil
}

func (s *Service) Undo(id int64) (api.Task, error) {
	t, err := s.store.Undo(id)
	if err != nil {
		return api.Task{}, err
	}
	s.changed()
	return api.FromTask(t), nil
}

func (s *Service) Delete(id int64) error {
	if err := s.store.Delete(id); err != nil {
		return err
	}
	s.changed()
	return nil
}

// Enrich puts a task back in front of the model on demand, which is the way
// out of a bad extraction: clear the field that is wrong and ask again.
func (s *Service) Enrich(id int64) (api.Task, error) {
	if s.enrich == nil {
		return api.Task{}, ErrNoModel
	}
	t, err := s.store.Get(id)
	if err != nil {
		return api.Task{}, err
	}
	s.queue(t.ID)
	return api.FromTask(t), nil
}

func (s *Service) Link(ctx context.Context, id int64, slug string) (api.Task, error) {
	if slug == "" {
		return api.Task{}, fmt.Errorf("link: slug is required: %w", store.ErrInvalid)
	}
	resolved, title, err := s.resolveLink(ctx, slug)
	if err != nil {
		return api.Task{}, err
	}
	return s.setLink(id, resolved, title)
}

// Unlink needs no vault. Cutting a link is ordo's own business, and it has to
// keep working when the vault is the thing that is broken.
func (s *Service) Unlink(id int64) (api.Task, error) { return s.setLink(id, "", "") }

func (s *Service) setLink(id int64, slug, title string) (api.Task, error) {
	t, err := s.store.Edit(id, store.Edit{MnemoSlug: &slug, MnemoTitle: &title})
	if err != nil {
		return api.Task{}, err
	}
	return api.FromTask(t), nil
}

// Related answers the one question worth asking the vault about a task, in
// whichever of its three states the task is: linked to a note that is there,
// linked to one that has gone, or not linked at all. The last two both want
// candidates, which is what makes repairing a renamed link the same gesture
// as making one.
func (s *Service) Related(ctx context.Context, id int64) (api.RelatedResponse, error) {
	t, err := s.store.Get(id)
	if err != nil {
		return api.RelatedResponse{}, err
	}
	if s.vault == nil {
		return api.RelatedResponse{}, ErrNoVault
	}

	if !t.Linked() {
		hits, err := s.vault.Search(ctx, t.Title, relatedLimit)
		if err != nil {
			return api.RelatedResponse{}, err
		}
		return api.RelatedResponse{Candidates: fromHits(hits)}, nil
	}

	note, err := s.vault.Get(ctx, t.MnemoSlug)
	if errors.Is(err, mnemo.ErrNotFound) {
		// The remembered description is a better search than the task title
		// here: it is what the note actually called itself before it moved.
		hits, searchErr := s.vault.Search(ctx, orphanQuery(t), relatedLimit)
		if searchErr != nil {
			return api.RelatedResponse{}, searchErr
		}
		return api.RelatedResponse{Linked: true, Missing: true, Candidates: fromHits(hits)}, nil
	}
	if err != nil {
		return api.RelatedResponse{}, err
	}

	// Similar is the one call that can be unavailable on its own, when the
	// vault has no embeddings. The note and its links are still worth having.
	similar, err := s.vault.Similar(ctx, t.MnemoSlug, relatedLimit)
	if err != nil {
		s.log.Warn("mnemo could not answer similar", "id", id, "slug", t.MnemoSlug, "error", err)
	}
	return api.RelatedResponse{
		Linked: true,
		Note: &api.Note{
			Slug:        note.Slug,
			Folder:      note.Folder,
			Description: note.Description,
			Tags:        note.Tags,
			Type:        note.Type,
		},
		Similar:   fromHits(similar),
		Links:     note.Links,
		Backlinks: note.Backlinks,
	}, nil
}

func (s *Service) Status() (api.StatusResponse, error) {
	sum, err := s.store.Summary()
	if err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{
		Open:       sum.Open,
		Done:       sum.Done,
		Overdue:    sum.Overdue,
		Unenriched: sum.Unenriched,
		Today:      store.Today(),
	}, nil
}

// resolveLink turns a slug into the pair ordo stores. It goes to the vault
// first, so a link can only ever be made to a note that exists, and so the
// description is captured at the moment of linking: that remembered text is
// the only way back to the note after a rename.
func (s *Service) resolveLink(ctx context.Context, slug string) (string, string, error) {
	if slug == "" {
		return "", "", nil
	}
	if s.vault == nil {
		return "", "", ErrNoVault
	}
	note, err := s.vault.Get(ctx, slug)
	if err != nil {
		return "", "", err
	}
	return note.Slug, note.Description, nil
}

func orphanQuery(t *store.Task) string {
	if t.MnemoTitle != "" {
		return t.MnemoTitle
	}
	return t.Title
}

// markOrphans asks the vault which of these links still resolve. A link is
// marked missing only on a definite answer: if the vault cannot be reached,
// or runs out of time, the links are left alone rather than all reported
// broken at once.
func (s *Service) markOrphans(ctx context.Context, tasks []api.Task) {
	if s.vault == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, orphanCheckTimeout)
	defer cancel()

	var wg sync.WaitGroup
	slots := make(chan struct{}, orphanCheckWorkers)
	for i := range tasks {
		if tasks[i].Mnemo == nil {
			continue
		}
		wg.Add(1)
		go func(link *api.Link) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			exists, err := s.vault.Exists(ctx, link.Slug)
			if err != nil {
				s.log.Warn("mnemo could not confirm a link", "slug", link.Slug, "error", err)
				return
			}
			link.Missing = !exists
		}(tasks[i].Mnemo)
	}
	wg.Wait()
}

// queue is nil-safe because enrichment is optional: with no model configured
// every surface still works, tasks just stay as they were typed.
func (s *Service) queue(id int64) {
	if s.enrich != nil {
		s.enrich.Queue(id)
	}
}

// changed is called after anything the plan is built from moves. It is
// nil-safe for the same reason queue is.
func (s *Service) changed() {
	if s.publish != nil {
		s.publish.Nudge()
	}
}

func fromHits(hits []mnemo.Hit) []api.Hit {
	if len(hits) == 0 {
		return nil
	}
	out := make([]api.Hit, 0, len(hits))
	for _, h := range hits {
		out = append(out, api.Hit{Slug: h.Slug, Folder: h.Folder, Description: h.Description, Score: h.Score})
	}
	return out
}
