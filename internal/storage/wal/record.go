package wal

import (
	"encoding/json"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
)

type EventType int

const (
	EventTaskCreated EventType = iota
	EventTaskStatus
	EventFileStatus
	EventFileMeta
)

type TaskCreatedPayload struct {
	Task *core.Task `json:"task"`
}

type TaskStatusPayload struct {
	Status core.TaskStatus `json:"status"`
}

type FileStatusPayload struct {
	FileIndex int             `json:"file_index"`
	Status    core.FileStatus `json:"status"`

	Error string `json:"error,omitempty"`
	Size  int64  `json:"size_bytes,omitempty"`
}

type FileMetaPayload struct {
	FileIndex int    `json:"file_index"`
	FileName  string `json:"file_name,omitempty"`

	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Event struct {
	Version int       `json:"version"`
	TaskID  string    `json:"task_id"`
	Type    EventType `json:"type"`

	CreatedAt time.Time `json:"created_at"`

	Payload json.RawMessage `json:"payload"`
}
