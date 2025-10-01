package api

import (
	"time"

	"github.com/mauzec/web-downloader/internal/core"
)

type CreateTaskRequest struct {
	URLs []string `json:"urls"`
}

type FileResponse struct {
	URL      string `json:"url"`
	Filename string `json:"file_name,omitempty"`
	Status   string `json:"status"`
	Size     int64  `json:"size_bytes,omitempty"`
	Error    string `json:"error,omitempty"`

	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}
type TaskResponse struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	URLs      []string        `json:"urls"`
	Files     []*FileResponse `json:"files"`
	CreatedAt *time.Time      `json:"created_at,omitempty"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}
type TasksListResponse struct {
	Tasks []*TaskResponse `json:"tasks"`
}

func NewTasksListResponse(tasks []*core.Task) *TasksListResponse {
	resp := &TasksListResponse{
		Tasks: make([]*TaskResponse, 0, len(tasks)),
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		resp.Tasks = append(resp.Tasks, NewTaskResponse(t))
	}
	return resp
}

func NewTaskResponse(task *core.Task) *TaskResponse {
	if task == nil {
		return nil
	}

	status := "unknown"
	switch task.Status {
	case core.TaskStatusCompleted:
		status = "completed"
	case core.TaskStatusFailed:
		status = "failed"
	case core.TaskStatusInProgress:
		status = "in_progress"
	case core.TaskStatusPending:
		status = "pending"
	case core.TaskStatusPartiallyCompleted:
		status = "partially_completed"
	}

	time1 := copyTime(task.CreatedAt)
	time2 := copyTime(task.UpdatedAt)

	resp := &TaskResponse{
		URLs:   task.URLs,
		ID:     task.ID,
		Status: status,

		CreatedAt: time1,
		UpdatedAt: time2,
	}
	if len(task.Files) > 0 {
		resp.Files = make([]*FileResponse, 0, len(task.Files))

		for _, f := range task.Files {
			if f == nil {
				resp.Files = append(resp.Files, &FileResponse{})
				continue
			}
			from, to := copyTime(f.StartedAt), copyTime(f.CompletedAt)
			resp.Files = append(resp.Files, &FileResponse{
				URL:      f.URL,
				Filename: f.Filename,
				Status: func() string {
					switch f.Status {
					case core.FileStatusCompleted:
						return "completed"
					case core.FileStatusError:
						return "error"
					case core.FileStatusQueued:
						return "queued"
					case core.FileStatusDownloading:
						return "downloading"
					case core.FileStatusWaiting:
						return "waiting"
					default:
						return "unknown"
					}
				}(),
				Size:  f.Size,
				Error: f.Error,

				StartedAt:   from,
				CompletedAt: to,
			})
		}
	}

	return resp
}

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	nt := *t
	return &nt
}
