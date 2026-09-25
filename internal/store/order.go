package store

import "sort"

// Sort applies the one ordering the app has: a repeating task already done
// today goes last, since there is nothing left to do on it; then overdue
// first; then deadline ascending with undated last; then priority; then
// difficulty, so easy work floats within a tier; then oldest first. The deadline is the due date, or
// the earlier one a task waiting on this one passed down. Every position is
// explainable from the fields that produced it, and nothing an LLM decides
// is persisted here.
func Sort(tasks []*Task) {
	sort.SliceStable(tasks, func(i, j int) bool { return less(tasks[i], tasks[j]) })
}

func less(a, b *Task) bool {
	if a.DoneToday != b.DoneToday {
		return b.DoneToday
	}
	if ao, bo := a.Overdue(), b.Overdue(); ao != bo {
		return ao
	}
	if ad, bd := a.Deadline(), b.Deadline(); ad != bd {
		// Undated sorts after every dated task.
		if ad == "" {
			return false
		}
		if bd == "" {
			return true
		}
		return ad < bd
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
