package storage

import (
	"context"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage/wal"
)

// TaskStore describes the interface for persisting and restoring downlder state.
// Implementations MUST be safe for concurrentcy and durable across restarts.
//
// TaskStore combines WAL for append-only event persistence and periodic snapshotting:
type TaskStore interface {
	// CreateTask MUST atomically write the <task creation> event to the WAL and make
	// the task available throuhg LoadAll.
	CreateTask(ctx context.Context, task *core.Task, event wal.Event) error
	// UpdateTask persists a set of derived events and
	// update last done representation used for snapshotting.
	UpdateTask(ctx context.Context, task *core.Task, events ...wal.Event) error
	// LoadAll reconstructs the complete set of tasks by loading the latest snapshot.
	// It should then apply all subsequent WAL events. The returned tasks sorted by created time.
	LoadAll(ctx context.Context) ([]*core.Task, error)

	Close() error
}
