package core

import (
	"sort"
	"time"
)

// Task is a group of files to download.
type Task struct {
	ID     string     `json:"id"`
	Files  []*File    `json:"files"`
	Status TaskStatus `json:"status"`
	URLs   []string   `json:"urls"`

	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func NewTask(id string, now *time.Time, urls ...string) *Task {
	files := make([]*File, 0, len(urls))
	for _, url := range urls {
		files = append(files, &File{URL: url, Status: FileStatusWaiting})
	}
	return &Task{
		ID:        id,
		Files:     files,
		Status:    TaskStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
		URLs:      append([]string(nil), urls...),
	}
}

// UpdateStatus obtains & updates task status (and UpdatedAt if changed).
func (t *Task) UpdateStatus(now *time.Time) {
	st := obtainStatus(t.Files)
	if st != t.Status {
		t.Status = st
		t.UpdatedAt = now
	}
}

func (t *Task) CloneTask() *Task {
	if t == nil {
		return nil
	}

	ct := *t
	if t.URLs != nil {
		ct.URLs = append([]string(nil), t.URLs...)
	}
	if t.Files != nil {
		ct.Files = make([]*File, 0, len(t.Files))
		for _, f := range t.Files {
			ct.Files = append(ct.Files, f.CloneFile())
		}
	}

	return &ct
}

func CloneTasks(tasks []*Task) []*Task {
	if len(tasks) == 0 {
		return nil
	}

	res := make([]*Task, 0, len(tasks))
	for _, t := range tasks {
		res = append(res, t.CloneTask())
	}
	return res
}

// SortTasks sorts tasks in-place by CreatedAt
func SortTasks(tasks []*Task) {
	sort.Slice(tasks, func(i, j int) bool {
		time1 := tasks[i].CreatedAt
		time2 := tasks[j].CreatedAt

		switch {
		case time1 == nil && time2 == nil:
			return tasks[i].ID < tasks[j].ID

		case time1 == nil:
			return false
		case time2 == nil:
			return true
		default:
			return time1.Before(*time2)
		}
	})
}

// File is a single file within a task.
type File struct {
	URL string `json:"url"`
	// Filename is the local name for saving. It can be empty until set.
	Filename string     `json:"file_name,omitempty"`
	Status   FileStatus `json:"status"`

	Size int64 `json:"size_bytes,omitempty"` // Final size after good download

	Error string `json:"error,omitempty"`

	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// IsFinished returns true if the file is in a final state.
func (f *File) IsFinished() bool {
	switch f.Status {
	case FileStatusCompleted, FileStatusError:
		return true
	default:
		return false
	}
}

func (f *File) CloneFile() *File {
	if f == nil {
		return nil
	}
	c := *f
	return &c
}
