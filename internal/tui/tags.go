package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// palette is the colours a badge can take: the terminal's own, so they sit
// with the rest of the screen in whatever theme it has, less the red that
// means overdue. Which one a tag gets is the daemon's decision, made over
// every tag in use so they are told apart; a note's colour is hashed from
// its slug, since notes are fewer and read in brackets anyway.
var palette = [store.Colours]lipgloss.Color{"2", "3", "4", "5", "6", "10", "11", "12", "13", "14"}

func colourAt(slot int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(palette[((slot%len(palette))+len(palette))%len(palette)])
}

func colourOf(word string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(word))
	return colourAt(int(h.Sum32() % uint32(len(palette))))
}

// colours is each tag's slot as the daemon gave it out.
func colours(tags []api.TagCount) map[string]int {
	out := map[string]int{}
	for _, t := range tags {
		out[t.Tag] = t.Colour
	}
	return out
}

// tagStyle is the colour a tag shows in: the daemon's where it has said,
// and hashed from the name until it has, so a tag just typed is coloured
// before the list comes back.
func (m Model) tagStyle(tag string) lipgloss.Style {
	if slot, ok := m.colours[tag]; ok {
		return colourAt(slot)
	}
	return colourOf(tag)
}

func (m Model) tag(tag string) string { return m.tagStyle(tag).Render("#" + tag) }

// badges is a row's tags in front of its title, each in its own colour, with
// the space that separates the last from the title.
func (m Model) badges(tags []string) string {
	var b strings.Builder
	for _, tag := range tags {
		b.WriteString(m.tag(tag))
		b.WriteString(" ")
	}
	return b.String()
}

// noteBadge is a linked note as it reads on a row and in the header.
func noteBadge(slug string) string {
	return colourOf(slug).Render("[[" + slug + "]]")
}
