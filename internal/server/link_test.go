package server

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/cameronpyne-smith/ordo/internal/api"
)

// vault serves a small fixed set of notes and records what was asked of it.
// The record is behind a mutex because the linked listing asks concurrently.
type asking struct {
	mu    sync.Mutex
	paths []string
}

func (a *asking) note(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paths = append(a.paths, path)
}

func (a *asking) all() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.paths...)
}

func vault(t *testing.T, notes map[string]string) (http.HandlerFunc, *asking) {
	t.Helper()
	asked := &asking{}
	return func(w http.ResponseWriter, r *http.Request) {
		asked.note(r.URL.Path + "?" + r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/similar"):
			w.Write([]byte(`{"results":[{"slug":"latent","description":"The company"}]}`))
		case r.URL.Path == "/search":
			w.Write([]byte(`{"results":[{"slug":"career-transition-quantitative-researcher","description":"Quant move","score":0.9}]}`))
		default:
			slug := strings.TrimPrefix(r.URL.Path, "/notes/")
			description, ok := notes[slug]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			w.Write([]byte(`{"slug":"` + slug + `","folder":"projects","description":"` + description +
				`","links":["latent"],"backlinks":["career"]}`))
		}
	}, asked
}

func TestLinkCapturesTheDescription(t *testing.T) {
	h, _ := newLinkedServer(t, mustVault(t, map[string]string{"latent": "The company"}))

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship the pricing model"})
	rec := request(t, h, http.MethodPost, "/tasks/1/link", api.LinkRequest{Slug: "latent"})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	var linked api.Task
	decodeInto(t, rec, &linked)
	if linked.Mnemo == nil || linked.Mnemo.Slug != "latent" || linked.Mnemo.Title != "The company" {
		t.Fatalf("link = %+v, want the slug and the note's description", linked.Mnemo)
	}
}

func TestLinkRefusesANoteThatIsNotThere(t *testing.T) {
	h, _ := newLinkedServer(t, mustVault(t, map[string]string{"latent": "The company"}))

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	rec := request(t, h, http.MethodPost, "/tasks/1/link", api.LinkRequest{Slug: "invented"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestCreateWithALink(t *testing.T) {
	h, _ := newLinkedServer(t, mustVault(t, map[string]string{"latent": "The company"}))

	rec := request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship it", MnemoSlug: "latent"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201: %s", rec.Code, rec.Body)
	}
	var created api.Task
	decodeInto(t, rec, &created)
	if created.Mnemo == nil || created.Mnemo.Title != "The company" {
		t.Fatalf("link = %+v", created.Mnemo)
	}
}

func TestUnlinkWorksWithoutAVault(t *testing.T) {
	h, _ := newLinkedServer(t, mustVault(t, map[string]string{"latent": "The company"}))

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship it", MnemoSlug: "latent"})
	rec := request(t, h, http.MethodPost, "/tasks/1/unlink", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	var unlinked api.Task
	decodeInto(t, rec, &unlinked)
	if unlinked.Mnemo != nil {
		t.Fatalf("link = %+v, want it gone", unlinked.Mnemo)
	}
}

func TestRelatedForALinkedTask(t *testing.T) {
	h, _ := newLinkedServer(t, mustVault(t, map[string]string{"latent": "The company"}))

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship it", MnemoSlug: "latent"})
	rec := request(t, h, http.MethodGet, "/tasks/1/related", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: %s", rec.Code, rec.Body)
	}
	var related api.RelatedResponse
	decodeInto(t, rec, &related)
	if !related.Linked || related.Missing || related.Note == nil {
		t.Fatalf("related = %+v, want a present note", related)
	}
	if len(related.Similar) != 1 || len(related.Links) != 1 || len(related.Backlinks) != 1 {
		t.Fatalf("related = %+v, want the neighbourhood", related)
	}
}

func TestRelatedForAnUnlinkedTaskOffersCandidates(t *testing.T) {
	h, _ := newLinkedServer(t, mustVault(t, map[string]string{"latent": "The company"}))

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Write the quant CV"})
	rec := request(t, h, http.MethodGet, "/tasks/1/related", nil)
	var related api.RelatedResponse
	decodeInto(t, rec, &related)
	if related.Linked || len(related.Candidates) != 1 {
		t.Fatalf("related = %+v, want candidates for an unlinked task", related)
	}
}

func TestRelatedReportsAnOrphanAndOffersTheWayBack(t *testing.T) {
	notes := map[string]string{"career-transition": "Quant move"}
	handler, asked := vault(t, notes)
	h, _ := newLinkedServer(t, handler)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Send the CV", MnemoSlug: "career-transition"})
	delete(notes, "career-transition")

	rec := request(t, h, http.MethodGet, "/tasks/1/related", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200: an orphan is a state, not an error", rec.Code)
	}
	var related api.RelatedResponse
	decodeInto(t, rec, &related)
	if !related.Linked || !related.Missing || len(related.Candidates) != 1 {
		t.Fatalf("related = %+v, want a missing note with candidates", related)
	}
	// The search that finds the note again must use what the note called
	// itself, not what the task is called.
	paths := asked.all()
	last := paths[len(paths)-1]
	if !strings.Contains(last, "q=Quant+move") {
		t.Fatalf("searched %q, want the remembered description", last)
	}
}

func TestLinkedListMarksOrphans(t *testing.T) {
	notes := map[string]string{"latent": "The company", "career-transition": "Quant move"}
	handler, _ := vault(t, notes)
	h, _ := newLinkedServer(t, handler)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship it", MnemoSlug: "latent"})
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Send the CV", MnemoSlug: "career-transition"})
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	delete(notes, "career-transition")

	rec := request(t, h, http.MethodGet, "/tasks?linked=true", nil)
	var list api.ListResponse
	decodeInto(t, rec, &list)
	if list.Count != 2 {
		t.Fatalf("count = %d, want only the linked tasks", list.Count)
	}
	missing := map[string]bool{}
	for _, task := range list.Tasks {
		missing[task.Mnemo.Slug] = task.Mnemo.Missing
	}
	if missing["latent"] || !missing["career-transition"] {
		t.Fatalf("missing = %v, want only the renamed note flagged", missing)
	}
}

func TestAPlainListDoesNotAskTheVault(t *testing.T) {
	handler, asked := vault(t, map[string]string{"latent": "The company"})
	h, _ := newLinkedServer(t, handler)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship it", MnemoSlug: "latent"})
	before := len(asked.all())
	request(t, h, http.MethodGet, "/tasks", nil)
	if after := len(asked.all()); after != before {
		t.Fatalf("the vault was asked %d times by a plain list, want none", after-before)
	}
}

func TestLinkRoutesSayWhenTheVaultIsOff(t *testing.T) {
	h, _, _ := newTestServer(t)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})
	if rec := request(t, h, http.MethodPost, "/tasks/1/link", api.LinkRequest{Slug: "latent"}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("link code = %d, want 503: %s", rec.Code, rec.Body)
	}
	if rec := request(t, h, http.MethodGet, "/tasks/1/related", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("related code = %d, want 503", rec.Code)
	}
	if rec := request(t, h, http.MethodPost, "/tasks/1/unlink", nil); rec.Code != http.StatusOK {
		t.Fatalf("unlink code = %d, want 200: cutting a link needs no vault", rec.Code)
	}
}

func mustVault(t *testing.T, notes map[string]string) http.HandlerFunc {
	t.Helper()
	h, _ := vault(t, notes)
	return h
}

func TestNoteFilterGroupsAWorkstream(t *testing.T) {
	notes := map[string]string{"career": "The quant plan", "latent": "The company"}
	handler, _ := vault(t, notes)
	h, _ := newLinkedServer(t, handler)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Wooldridge ch. 2", MnemoSlug: "career"})
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Green Book drill", MnemoSlug: "career"})
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Ship the pricing model", MnemoSlug: "latent"})
	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Bins"})

	rec := request(t, h, http.MethodGet, "/tasks?note=career", nil)
	var list api.ListResponse
	decodeInto(t, rec, &list)
	if list.Count != 2 {
		t.Fatalf("count = %d, want the two tasks for that note", list.Count)
	}
	for _, task := range list.Tasks {
		if task.Mnemo.Slug != "career" {
			t.Fatalf("got %q, want only the named note", task.Mnemo.Slug)
		}
	}
}

func TestNoteFilterChecksTheNoteToo(t *testing.T) {
	notes := map[string]string{"career": "The quant plan"}
	handler, _ := vault(t, notes)
	h, _ := newLinkedServer(t, handler)

	request(t, h, http.MethodPost, "/tasks", api.CreateRequest{Title: "Wooldridge ch. 2", MnemoSlug: "career"})
	delete(notes, "career")

	rec := request(t, h, http.MethodGet, "/tasks?note=career", nil)
	var list api.ListResponse
	decodeInto(t, rec, &list)
	if list.Count != 1 || !list.Tasks[0].Mnemo.Missing {
		t.Fatalf("got %+v, want the renamed note flagged", list.Tasks)
	}
}
