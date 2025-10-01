package storage_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage"
	"github.com/mauzec/web-downloader/internal/storage/wal"
	"github.com/stretchr/testify/require"
)

func TestFileTaskStore_CreateUpdateRecover(t *testing.T) {
	t.Parallel()
	var (
		ctx     = context.Background()
		ssPath  = filepath.Join(t.TempDir(), "snapshot.json")
		walPath = filepath.Join(t.TempDir(), "wal.log")
	)

	store, err := storage.NewFileTaskStore(ssPath, walPath)
	require.NoErrorf(t, err, "newstore error: %v", err)
	defer store.Close()

	tasks, err := store.LoadAll(ctx)
	require.NoErrorf(t, err, "first loadall: %v", err)
	require.Lenf(t, tasks, 0, "want 0 tasks, got %d", len(tasks))

	tim := time.Now().UTC()
	task := core.NewTask("simon", &tim, "https://docs.google.com/ss/simon")

	createdPayload, err := json.Marshal(wal.TaskCreatedPayload{Task: task})
	require.NoErrorf(t, err, "marshal created_payload: %v", err)

	createdEvent := wal.Event{
		CreatedAt: tim,
		Type:      wal.EventTaskCreated,
		Version:   1,
		TaskID:    task.ID,
		Payload:   createdPayload,
	}
	require.NoErrorf(t, store.CreateTask(ctx, task, createdEvent),
		"createtask: %v", err,
	)

	upd := cloneForTest(task)
	upd.Files[0].Status = core.FileStatusCompleted
	upd.Files[0].Filename = "simon.ini"
	upd.Files[0].Size = 512
	upd.Status = core.TaskStatusCompleted
	deltatim := tim.Add(2 * time.Second)
	upd.Files[0].CompletedAt = &deltatim
	upd.UpdatedAt = &deltatim

	statusPayload, err := json.Marshal(wal.FileStatusPayload{
		FileIndex: 0, Status: core.FileStatusCompleted, Size: 512,
	})
	require.NoErrorf(t, err, "marshal status_payload: %v", err)

	metadataPayload, err := json.Marshal(wal.FileMetaPayload{
		FileIndex: 0, FileName: "simon.ini", CompletedAt: &deltatim,
	})
	require.NoErrorf(t, err, "marshal meta_payload: %v", err)

	statusEvent := wal.Event{
		CreatedAt: deltatim,
		Version:   1,
		TaskID:    task.ID,
		Type:      wal.EventFileStatus,
		Payload:   statusPayload,
	}
	metadataEvent := wal.Event{
		CreatedAt: deltatim,
		Version:   1,
		TaskID:    task.ID,
		Type:      wal.EventFileMeta,

		Payload: metadataPayload,
	}

	require.NoErrorf(t,
		store.UpdateTask(ctx, upd, statusEvent, metadataEvent),
		"updatetask marshal: %v", err,
	)
	require.NoErrorf(t, store.FlushSnapshot(ctx),
		"flush snapshot: %v", err,
	)
	require.NoErrorf(t, store.Close(), "close: %v", err)

	info, err := os.Stat(walPath)
	require.NoError(t, err)
	require.Zero(t, info.Size(), "wal should be empty after snapsho flush")

	oldDir := filepath.Join(filepath.Dir(walPath), "old")
	entries, err := os.ReadDir(oldDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected a single old wal file")

	// reconstruct

	recStore, err := storage.NewFileTaskStore(ssPath, walPath)
	require.NoErrorf(t, err, "newstore reopen error: %v", err)
	defer recStore.Close()

	restoredTasks, err := recStore.LoadAll(ctx)
	require.NoErrorf(t, err, "loadall after 2nd open: %v", err)
	require.Lenf(t, restoredTasks, 1,
		"want 1 restored task, got %d", len(restoredTasks),
	)
	recTask := restoredTasks[0]
	require.Equalf(t, core.TaskStatusCompleted, recTask.Status,
		"recovered task status: got %v, want %v",
		recTask.Status, core.TaskStatusCompleted,
	)
	require.Equalf(t, 1, len(recTask.Files),
		"recovered task files len = %d", len(recTask.Files),
	)

	f := recTask.Files[0]
	require.Equalf(t, core.FileStatusCompleted, f.Status,
		"recovered file status: got %v, want %v",
		f.Status, core.FileStatusCompleted,
	)
	require.Equalf(t, "simon.ini", f.Filename,
		"recovered file name: got %q, want simon.ini",
		f.Filename,
	)
	require.Equalf(t, int64(512), f.Size,
		"recovered file size: got %d, want 512",
		f.Size,
	)
	require.NotNilf(t, f.CompletedAt, "recovered file completedat is nil")
	require.Truef(t, f.CompletedAt.Equal(deltatim),
		"recovered file completeat: got %v, want %v",
		f.CompletedAt, deltatim,
	)

}

func cloneForTest(task *core.Task) *core.Task {
	copy := *task
	copy.Files = make([]*core.File, len(task.Files))
	copy.URLs = append([]string(nil), task.URLs...)

	for i, f := range task.Files {
		fc := *f
		copy.Files[i] = &fc
	}

	return &copy
}
