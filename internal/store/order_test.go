package store

import (
	"testing"
	"time"
)

func fixedNow(t *testing.T, date string) {
	t.Helper()
	when, err := time.ParseInLocation(time.RFC3339, date, Location)
	if err != nil {
		t.Fatalf("parsing %s: %v", date, err)
	}
	original := Now
	Now = func() time.Time { return when }
	t.Cleanup(func() { Now = original })
}

func task(id int64, mutate func(*Task)) *Task {
	t := &Task{ID: id, Title: "t", Status: StatusOpen, CreatedAt: time.Unix(id, 0).In(Location)}
	if mutate != nil {
		mutate(t)
	}
	return t
}

func TestSort(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")

	tests := []struct {
		name  string
		tasks []*Task
		want  []int64
	}{
		{
			name: "overdue before everything else",
			tasks: []*Task{
				task(1, func(t *Task) { t.Priority = PriorityHigh }),
				task(2, func(t *Task) { t.Due = "2026-09-19"; t.Priority = PriorityLow }),
			},
			want: []int64{2, 1},
		},
		{
			name: "earlier due date wins",
			tasks: []*Task{
				task(1, func(t *Task) { t.Due = "2026-09-25" }),
				task(2, func(t *Task) { t.Due = "2026-09-21" }),
			},
			want: []int64{2, 1},
		},
		{
			name: "due beats priority",
			tasks: []*Task{
				task(1, func(t *Task) { t.Priority = PriorityHigh }),
				task(2, func(t *Task) { t.Due = "2026-09-30"; t.Priority = PriorityLow }),
			},
			want: []int64{2, 1},
		},
		{
			name: "undated sorts after dated",
			tasks: []*Task{
				task(1, nil),
				task(2, func(t *Task) { t.Due = "2027-01-01" }),
			},
			want: []int64{2, 1},
		},
		{
			name: "priority orders undated tasks",
			tasks: []*Task{
				task(1, func(t *Task) { t.Priority = PriorityLow }),
				task(2, func(t *Task) { t.Priority = PriorityHigh }),
				task(3, nil),
			},
			want: []int64{2, 3, 1},
		},
		{
			name: "unset priority ranks as normal",
			tasks: []*Task{
				task(1, func(t *Task) { t.Priority = PriorityNormal }),
				task(2, nil),
			},
			want: []int64{1, 2},
		},
		{
			name: "quick wins float within a tier",
			tasks: []*Task{
				task(1, func(t *Task) { t.Difficulty = DifficultyHigh }),
				task(2, func(t *Task) { t.Difficulty = DifficultyLow }),
				task(3, func(t *Task) { t.Difficulty = DifficultyMedium }),
			},
			want: []int64{2, 3, 1},
		},
		{
			name: "unknown difficulty sorts last",
			tasks: []*Task{
				task(1, nil),
				task(2, func(t *Task) { t.Difficulty = DifficultyHigh }),
			},
			want: []int64{2, 1},
		},
		{
			name: "oldest first when everything else ties",
			tasks: []*Task{
				task(7, nil),
				task(3, nil),
			},
			want: []int64{3, 7},
		},
		{
			name: "earlier overdue date comes first",
			tasks: []*Task{
				task(1, func(t *Task) { t.Due = "2026-09-19" }),
				task(2, func(t *Task) { t.Due = "2026-09-01" }),
			},
			want: []int64{2, 1},
		},
		{
			name: "a done task is never overdue",
			tasks: []*Task{
				task(1, func(t *Task) { t.Due = "2026-09-19"; t.Status = StatusDone }),
				task(2, func(t *Task) { t.Due = "2026-09-19" }),
			},
			want: []int64{2, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Sort(tt.tasks)
			got := make([]int64, len(tt.tasks))
			for i, task := range tt.tasks {
				got[i] = task.ID
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("order = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestOverdue(t *testing.T) {
	fixedNow(t, "2026-09-20T09:00:00Z")

	tests := []struct {
		name string
		task *Task
		want bool
	}{
		{"yesterday", task(1, func(t *Task) { t.Due = "2026-09-19" }), true},
		{"today is not overdue", task(1, func(t *Task) { t.Due = "2026-09-20" }), false},
		{"tomorrow", task(1, func(t *Task) { t.Due = "2026-09-21" }), false},
		{"undated", task(1, nil), false},
		{"done", task(1, func(t *Task) { t.Due = "2026-01-01"; t.Status = StatusDone }), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.Overdue(); got != tt.want {
				t.Fatalf("Overdue() = %v, want %v", got, tt.want)
			}
		})
	}
}
