package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/client"
)

func tap(t *testing.T, m Model, key string) Model {
	t.Helper()
	m, _ = press(t, m, key)
	return m
}

// The numbered keys toggle fields of one filter and combine; 1 clears them.
func TestFiltersCombineAndClear(t *testing.T) {
	m := New(nil)
	m = tap(t, tap(t, m, "2"), "4")
	if want := (client.Filter{Overdue: true, Priority: "high"}); m.active != want {
		t.Fatalf("after 2 and 4, filter = %+v, want %+v", m.active, want)
	}
	if got := m.filterName(); got != "overdue · high priority" {
		t.Fatalf("header names %q", got)
	}
	m = tap(t, m, "2")
	if m.active.Overdue {
		t.Fatal("2 again should have cleared overdue")
	}
	m.active.Tag = "house"
	m = tap(t, m, "1")
	if m.active != (client.Filter{}) {
		t.Fatalf("1 should clear everything, got %+v", m.active)
	}
	if got := m.filterName(); got != "open" {
		t.Fatalf("an empty filter is named %q, want open", got)
	}
}

// The chooser narrows to a tag or a note, and choosing the active one
// again clears it.
func TestChooserSetsAndClearsTheTag(t *testing.T) {
	m := New(nil)
	next, _ := m.openChoose(chooseTag)
	m = next.(Model)
	if !strings.Contains(m.View(), "asking the daemon") {
		t.Fatal("the chooser should say it is waiting for the tags")
	}
	m = m.applyChoose(chooseMsg{choices: tagChoices([]api.TagCount{{Tag: "career", Open: 2}, {Tag: "house", Open: 3}})})
	if !strings.Contains(m.View(), "#house") || !strings.Contains(m.View(), "3 open") {
		t.Fatalf("the chooser should list the tags with counts:\n%s", m.View())
	}
	next, _ = m.keyChoose(tea.KeyMsg{Type: tea.KeyDown})
	next, _ = next.(Model).keyChoose(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.active.Tag != "house" || m.mode != modeList {
		t.Fatalf("after enter: tag = %q mode = %d", m.active.Tag, m.mode)
	}
	if !strings.Contains(m.filterName(), "#house") {
		t.Fatalf("header names %q", m.filterName())
	}

	next, _ = m.openChoose(chooseTag)
	m = next.(Model).applyChoose(chooseMsg{choices: tagChoices([]api.TagCount{{Tag: "house", Open: 3}})})
	if !strings.Contains(m.View(), "[x]") {
		t.Fatal("the active tag should be ticked")
	}
	next, _ = m.keyChoose(tea.KeyMsg{Type: tea.KeyEnter})
	if m = next.(Model); m.active.Tag != "" {
		t.Fatalf("choosing the active tag again should clear it, got %q", m.active.Tag)
	}
}

func TestNoteChoicesCountTheOpenTasksPerNote(t *testing.T) {
	linked := func(slug, title string) func(*api.Task) {
		return func(t *api.Task) { t.Mnemo = &api.Link{Slug: slug, Title: title} }
	}
	got := noteChoices([]api.Task{
		task(1, "a", linked("career", "The quant plan")),
		task(2, "b"),
		task(3, "c", linked("latent", "The company")),
		task(4, "d", linked("career", "The quant plan")),
	})
	if len(got) != 2 || got[0].value != "career" || got[0].count != 2 || got[0].detail != "The quant plan" || got[1].value != "latent" {
		t.Fatalf("choices = %+v", got)
	}
}

// Tags read before the title on a row, and a tag is found by search.
func TestTagsShowBeforeTheTitleAndAreSearchable(t *testing.T) {
	m := New(nil)
	m.width, m.height = 80, 24
	m = m.applyTasks(tasksMsg{resp: &api.ListResponse{Tasks: []api.Task{
		task(1, "Paint the living room", func(x *api.Task) { x.Tags = []string{"house", "reno"} }),
		task(2, "Send the CV"),
	}}})
	row := m.renderRow(1, 80)
	house, title := strings.Index(row, "#house"), strings.Index(row, "Paint")
	if house < 0 || title < 0 || house > title {
		t.Fatalf("row = %q, want #house before the title", row)
	}
	if !strings.Contains(m.renderRow(2, 80), "Send") || strings.Contains(m.renderRow(2, 80), "#") {
		t.Fatalf("an untagged row should carry no badge: %q", m.renderRow(2, 80))
	}
	if !matches(m.tasks[0], "reno") || matches(m.tasks[1], "reno") {
		t.Fatal("search should find a task by its tag")
	}
}

func TestTagsEditAsOneLine(t *testing.T) {
	req, err := editRequest("tags", "house, #reno career")
	if err != nil {
		t.Fatal(err)
	}
	if req.Tags == nil || strings.Join(*req.Tags, "|") != "house|#reno|career" {
		t.Fatalf("tags = %v", req.Tags)
	}
	req, _ = editRequest("tags", "")
	if req.Tags == nil || len(*req.Tags) != 0 {
		t.Fatalf("an empty line should clear the tags, got %v", req.Tags)
	}
}

// A tag's colour comes from the tag alone, so it is the same everywhere.
func TestATagKeepsItsColour(t *testing.T) {
	if colourOf("house").GetForeground() != colourOf("house").GetForeground() {
		t.Fatal("the same tag got two colours")
	}
	different := false
	for _, other := range []string{"career", "reno", "diy", "admin", "garden"} {
		if colourOf(other).GetForeground() != colourOf("house").GetForeground() {
			different = true
		}
	}
	if !different {
		t.Fatal("every tag got the same colour")
	}
}
