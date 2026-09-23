package store

import "sort"

// Sort applies the one ordering the app has: overdue first; then due
// ascending with undated last; then priority; then difficulty, so easy work
// floats within a tier; then oldest first. Every position is explainable from
// the fields that produced it, and nothing an LLM decides is persisted here.
func Sort(tasks []*Task) {
	sort.SliceStable(tasks, func(i, j int) bool { return less(tasks[i], tasks[j]) })
}

func less(a, b *Task) bool {
	if ao, bo := a.Overdue(), b.Overdue(); ao != bo {
		return ao
	}
	if a.Due != b.Due {
		// Undated sorts after every dated task.
		if a.Due == "" {
			return false
		}
		if b.Due == "" {
			return true
		}
		return a.Due < b.Due
	}
	if ar, br := priorityRank(a.EffectivePriority()), priorityRank(b.EffectivePriority()); ar != br {
		return ar < br
	}
	if ar, br := difficultyRank(a.Difficulty), difficultyRank(b.Difficulty); ar != br {
		return ar < br
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func priorityRank(p Priority) int {
	switch p {
	case PriorityHigh:
		return 0
	case PriorityNormal:
		return 1
	case PriorityLow:
		return 2
	}
	return 1
}

// Unknown difficulty sorts after every known one: an unenriched task should
// not masquerade as a quick win.
func difficultyRank(d Difficulty) int {
	switch d {
	case DifficultyLow:
		return 0
	case DifficultyMedium:
		return 1
	case DifficultyHigh:
		return 2
	}
	return 3
}
