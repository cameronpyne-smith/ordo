package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cameronpyne-smith/ordo/internal/api"
	"github.com/cameronpyne-smith/ordo/internal/store"
)

// The terminal's own colours each mean one thing here, and a tag must not
// be mistaken for any of them:
//
//	red          1   overdue, and an error in the footer
//	green        2   done: a finished row, and a repeating task done today
//	yellow       3   a link whose note has gone
//	magenta      5   section headings
//	bright blue  12  a link to a note
//	white/grey       ordinary and faint text
//
// So the tags take fixed colours from the 256-colour table instead, chosen
// to be told apart from those and from each other, and readable on a dark
// or a light background. They do not follow the terminal's theme, which is
// the price of ten colours none of which is a meaning already taken. Which
// one a tag gets is the daemon's decision, made over every tag in use.
var palette = [store.Colours]lipgloss.Color{
	"208", // orange
	"33",  // azure
	"135", // violet
	"211", // pink
	"130", // brown
	"67",  // slate blue
	"183", // lavender
	"216", // apricot
	"63",  // indigo
	"132", // plum
}

func colourAt(slot int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(palette[((slot%len(palette))+len(palette))%len(palette)])
}

// colourOf is a colour from the word alone, for a tag the daemon has not
// placed yet.
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

// noteBadge is a linked note as it reads on a row and in the header: always
// the link colour, so a note is never taken for a tag.
func noteBadge(slug string) string {
	return linkStyle.Render("[[" + slug + "]]")
}
