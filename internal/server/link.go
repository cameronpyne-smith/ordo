package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/cameronpyne-smith/ordo/internal/api"
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

var errNoVault = errors.New("mnemo is not configured: set [mnemo] url and token on the daemon")

// resolveLink turns a slug into the pair ordo stores. It goes to the vault
// first, so a link can only ever be made to a note that exists, and so the
// description is captured at the moment of linking: that remembered text is
// the only way back to the note after a rename.
func (s *Server) resolveLink(ctx context.Context, slug string) (string, string, error) {
	if slug == "" {
		return "", "", nil
	}
	if s.vault == nil {
		return "", "", errNoVault
	}
	note, err := s.vault.Get(ctx, slug)
	if err != nil {
		return "", "", err
	}
	return note.Slug, note.Description, nil
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req api.LinkRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Slug == "" {
		writeError(w, http.StatusBadRequest, errors.New("slug is required"))
		return
	}
	slug, title, err := s.resolveLink(r.Context(), req.Slug)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	t, err := s.store.Edit(id, store.Edit{MnemoSlug: &slug, MnemoTitle: &title})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.FromTask(t))
}

// handleUnlink needs no vault. Cutting a link is ordo's own business, and it
// has to keep working when the vault is the thing that is broken.
func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	none := ""
	t, err := s.store.Edit(id, store.Edit{MnemoSlug: &none, MnemoTitle: &none})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, api.FromTask(t))
}

// handleRelated answers the one question worth asking the vault about a task,
// in whichever of its three states the task is: linked to a note that is
// there, linked to one that has gone, or not linked at all. The last two both
// want candidates, which is what makes relinking after a rename the same
// gesture as linking in the first place.
func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	id, err := taskID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.store.Get(id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, errNoVault)
		return
	}
	ctx := r.Context()

	if !t.Linked() {
		hits, err := s.vault.Search(ctx, t.Title, relatedLimit)
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, api.RelatedResponse{Candidates: fromHits(hits)})
		return
	}

	note, err := s.vault.Get(ctx, t.MnemoSlug)
	if errors.Is(err, mnemo.ErrNotFound) {
		// The remembered description is a better search than the task title
		// here: it is what the note actually called itself before it moved.
		hits, searchErr := s.vault.Search(ctx, orphanQuery(t), relatedLimit)
		if searchErr != nil {
			writeError(w, statusFor(searchErr), searchErr)
			return
		}
		writeJSON(w, http.StatusOK, api.RelatedResponse{
			Linked: true, Missing: true, Candidates: fromHits(hits),
		})
		return
	}
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}

	// Similar is the one call that can be unavailable on its own, when the
	// vault has no embeddings. The note and its links are still worth having.
	similar, err := s.vault.Similar(ctx, t.MnemoSlug, relatedLimit)
	if err != nil {
		s.log.Warn("mnemo could not answer similar", "id", id, "slug", t.MnemoSlug, "error", err)
	}
	writeJSON(w, http.StatusOK, api.RelatedResponse{
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
	})
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
func (s *Server) markOrphans(ctx context.Context, tasks []api.Task) {
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
