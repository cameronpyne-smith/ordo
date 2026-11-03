package tui

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// palette is the colours a badge can take: the terminal's own, so they sit
// with the rest of the screen in whatever theme it has, less the red that
// means overdue. A word's colour comes from hashing it, so a tag or a note
// has the same colour on every machine and every run without anything
// being stored.
var palette = []lipgloss.Color{"2", "3", "4", "5", "6", "10", "11", "12", "13", "14"}

func colourOf(word string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(word))
	return lipgloss.NewStyle().Foreground(palette[h.Sum32()%uint32(len(palette))])
}

// badges is a row's tags in front of its title, each in its own colour, with
// the space that separates the last from the title.
func badges(tags []string) string {
	var b strings.Builder
	for _, tag := range tags {
		b.WriteString(colourOf(tag).Render("#" + tag))
		b.WriteString(" ")
	}
	return b.String()
}

// noteBadge is a linked note as it reads on a row and in the header.
func noteBadge(slug string) string {
	return colourOf(slug).Render("[[" + slug + "]]")
}
