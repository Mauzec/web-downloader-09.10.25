package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage"
	"github.com/stretchr/testify/require"
)

type stubIDGen struct {
	mu  sync.Mutex
	ids []string
}

func (g *stubIDGen) NewID() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.ids) == 0 {
		return "", errors.New("no ids")
	}
	id := g.ids[0]
	g.ids = g.ids[1:]
	return id, nil
}

type stubClock struct {
	mu   sync.Mutex
	curr time.Time
	step time.Duration
}

func newStubClock(start time.Time, step time.Duration) *stubClock {
	return &stubClock{curr: start, step: step}
}

func (c *stubClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.curr
	c.curr = c.curr.Add(c.step)
	return t
}

func newTestServiceWithClock(t *testing.T, start time.Time, step time.Duration, ids ...string) (*TaskService, storage.TaskStore) {
	t.Helper()
	sPath := filepath.Join(t.TempDir(), "snapshot.json")
	lPath := filepath.Join(t.TempDir(), "wal.log")
	store, err := storage.NewFileTaskStore(sPath, lPath)
	require.NoError(t, err)

	clock := newStubClock(start, step)
	idList := append([]string(nil), ids...)
	if len(idList) == 0 {
		idList = []string{"simon-t"}
	}
	svc, svcErr := NewTaskService(
		context.Background(),
		store,
		store,
		&stubIDGen{ids: idList},
		clock.Now,
	)
	require.NoError(t, svcErr)

	return svc, store
}

func newTestService(t *testing.T, id string) (*TaskService, storage.TaskStore) {
	return newTestServiceWithClock(t, time.Now().Add(time.Second+2), 30*time.Second, id)
}

func newBoltTestService(t *testing.T, ids ...string) (*TaskService, storage.TaskStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	store, err := storage.NewBoltTaskStore(dbPath)
	require.NoError(t, err)

	idList := append([]string(nil), ids...)
	if len(idList) == 0 {
		idList = []string{"simon-bolt-task"}
	}

	svc, svcErr := NewTaskService(
		context.Background(),
		store,
		store,
		&stubIDGen{ids: idList},
		time.Now,
	)
	require.NoError(t, svcErr)

	return svc, store
}

func TestTaskService_CreateTask(t *testing.T) {
	svc, store := newTestService(t, "simon")
	defer store.Close()

	task, err := svc.CreateTask(context.Background(),
		[]string{" https://docs.google.com/ss/simon1 ", // it will be normalized
			"https://docs.google.com/ss/simon2",
		},
	)
	require.NoError(t, err)

	require.Equal(t, "simon", task.ID)
	require.Equal(t, core.TaskStatusPending, task.Status)
	require.Len(t, task.Files, 2)
	require.Equal(t, "https://docs.google.com/ss/simon1", task.Files[0].URL)
	require.Equal(t, core.FileStatusWaiting, task.Files[0].Status)
	require.NotNil(t, task.CreatedAt)
	require.NotNil(t, task.UpdatedAt)

	// test that task from get task is a clone
	task.Files[0].Status = core.FileStatusError

	stored, getErr := svc.GetTask(context.Background(), "simon")
	require.NoError(t, getErr)
	require.Equal(t, core.FileStatusWaiting, stored.Files[0].Status)

	loaded, loadErr := store.LoadAll(context.Background())
	require.NoError(t, loadErr)
	require.Len(t, loaded, 1)
	require.Equal(t, core.TaskStatusPending, loaded[0].Status)
}

func TestTaskService_CreateTaskBadUrl(t *testing.T) {
	svc, store := newTestService(t, "simon")
	defer store.Close()

	_, err := svc.CreateTask(context.Background(), []string{"     ", ""})
	require.Error(t, err)
	appErr, ok := err.(*core.AppError)
	require.True(t, ok)
	require.Equal(t, core.ErrorCodeValidation, appErr.Code)
}

func TestTaskService_TaskFileLiving(t *testing.T) {
	svc, store := newTestService(t, "simon")
	defer store.Close()

	ctx := context.Background()

	task, err := svc.CreateTask(ctx,
		[]string{" https://docs.google.com/ss/simon1 ", // it will be normalized
			"https://docs.google.com/ss/simon2",
		},
	)
	require.NoError(t, err)

	// task 1
	task, err = svc.MarkFileStarted(ctx, task.ID, 0)
	require.NoError(t, err)
	require.Equal(t, core.TaskStatusInProgress, task.Status)
	require.Equal(t, core.FileStatusDownloading, task.Files[0].Status)
	task, err = svc.MarkFileCompleted(ctx,
		task.ID, 0, "simon-file.simon", 1024,
	)
	require.NoError(t, err)
	require.Equal(t, core.TaskStatusInProgress, task.Status)
	require.Equal(t, core.FileStatusCompleted, task.Files[0].Status)
	require.Equal(t, int64(1024), task.Files[0].Size)
	require.Equal(t, "simon-file.simon", task.Files[0].Filename)

	task, err = svc.MarkFileStarted(ctx, task.ID, 1)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusDownloading, task.Files[1].Status)

	// task 2 fails
	task, err = svc.MarkFileFailed(ctx, task.ID, 1, "simon falled")
	require.NoError(t, err)
	require.Equal(t, core.TaskStatusPartiallyCompleted, task.Status)
	require.Equal(t, core.FileStatusError, task.Files[1].Status)
	require.Equal(t, "simon falled", task.Files[1].Error)

	loaded, loadErr := store.LoadAll(ctx)
	require.NoError(t, loadErr)
	require.Len(t, loaded, 1)
	require.Equal(t, core.TaskStatusPartiallyCompleted, loaded[0].Status)
	require.Equal(t, core.FileStatusCompleted, loaded[0].Files[0].Status)
	require.Equal(t, core.FileStatusError, loaded[0].Files[1].Status)
}

func TestTaskService_BoltWithoutCache(t *testing.T) {
	svc, store := newBoltTestService(t, "bolt-1")
	defer store.Close()

	require.False(t, svc.useCache)
	require.Nil(t, svc.tasks)

	ctx := context.Background()

	task, err := svc.CreateTask(ctx, []string{
		"https://docs.google.com/1",
		"https://docs.google.com/2",
	})
	require.NoError(t, err)
	require.Equal(t, "bolt-1", task.ID)
	require.NotNil(t, task.CreatedAt)
	require.Equal(t, core.FileStatusWaiting, task.Files[0].Status)
	require.Nil(t, svc.tasks)

	task, err = svc.MarkFileQueued(ctx, task.ID, 0)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusQueued, task.Files[0].Status)
	task, err = svc.MarkFileStarted(ctx, task.ID, 0)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusDownloading, task.Files[0].Status)
	task, err = svc.MarkFileCompleted(ctx, task.ID, 0, "simon.data", 1024)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusCompleted, task.Files[0].Status)
	require.Equal(t, int64(1024), task.Files[0].Size)
	task, err = svc.MarkFileFailed(ctx, task.ID, 1, "network error")
	require.NoError(t, err)
	require.Equal(t, core.FileStatusError, task.Files[1].Status)

	stored, err := svc.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusCompleted, stored.Files[0].Status)
	require.Equal(t, core.FileStatusError, stored.Files[1].Status)
	list, err := svc.SortedTasks(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, task.ID, list[0].ID)
	require.Equal(t, core.FileStatusCompleted, list[0].Files[0].Status)
	require.Equal(t, core.FileStatusError, list[0].Files[1].Status)
}
