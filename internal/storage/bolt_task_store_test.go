package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage/wal"
	"github.com/stretchr/testify/require"
)

func TestBoltTaskStore_CreateUpdateLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	store, err := NewBoltTaskStore(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	ctx := context.Background()
	now := time.Now().UTC()
	task := core.NewTask("simon-1", &now, "http://google.com/simon.data")
	err = store.CreateTask(ctx, task, wal.Event{})
	require.NoError(t, err)

	ts, err := store.LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, ts, 1)
	require.Equal(t, task.ID, ts[0].ID)
	require.Equal(t, core.TaskStatusPending, ts[0].Status)

	task.Status = core.TaskStatusCompleted
	next := now.Add(time.Minute)
	task.UpdatedAt = &next
	err = store.UpdateTask(ctx, task, wal.Event{})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store, err = NewBoltTaskStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	///----

	ts, err = store.LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, ts, 1)
	require.Equal(t, core.TaskStatusCompleted, ts[0].Status)
}
