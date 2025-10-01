package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/worker"
	"github.com/stretchr/testify/require"
)

type mockJobSubmitter struct {
	mu   sync.Mutex
	jobs []worker.Job
}

func (mjb *mockJobSubmitter) Submit(ctx context.Context, job worker.Job) error {
	mjb.mu.Lock()
	mjb.jobs = append(mjb.jobs, job)
	mjb.mu.Unlock()
	return nil
}

func (mjb *mockJobSubmitter) Stop() {}

func TestDownloadManagerUpToComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("sailor"))
	}))
	t.Cleanup(server.Close)

	svc, store := newTestServiceWithClock(t,
		time.Now().Add(2*time.Minute),
		time.Millisecond*10,
	)
	t.Cleanup(func() { _ = store.Close() })

	task, err := svc.CreateTask(ctx, []string{server.URL + "/simon.data"})
	require.NoError(t, err)

	opts := DownloadManagerOptions{
		HTTPClient:           server.Client(),
		Dir:                  filepath.Join(dir, "files"),
		QueueSize:            6,
		MaxCPU:               2,
		DownloadTimeout:      time.Second,
		UserAgent:            "test-agent",
		MaxAttempts:          1,
		BackoffDuration:      10 * time.Millisecond,
		RetryEnqueueInterval: 20 * time.Millisecond,
	}
	mngr, err := NewDownloadManager(ctx, svc, &opts)
	require.NoError(t, err)
	t.Cleanup(mngr.Close)

	require.NoError(t, mngr.Enqueue(ctx, task.ID))
	require.Eventually(t, func() bool {
		last, err := svc.GetTask(ctx, task.ID)
		if err != nil || len(last.Files) == 0 {
			return false
		}
		return last.Files[0].Status == core.FileStatusCompleted

	}, 5*time.Second, 50*time.Millisecond,
	)
}

func TestDownloadManagerEnqueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, store := newTestServiceWithClock(t, time.Now().Add(2*time.Minute), time.Millisecond*10)
	t.Cleanup(func() { _ = store.Close() })

	disp := &mockJobSubmitter{}
	mngr := &DownloadManager{pool: disp, svc: svc}

	task, err := svc.CreateTask(ctx, []string{
		"https://docs.google.com/simon1",
		"https://docs.google.com/simon2",
		"https://docs.google.com/simon3",
		"https://docs.google.com/simon4",
	})
	require.NoError(t, err)

	_, err = svc.MarkFileCompleted(ctx, task.ID, 0, "s1", 10)
	require.NoError(t, err)
	_, err = svc.MarkFileStarted(ctx, task.ID, 1)
	require.NoError(t, err)
	_, err = svc.MarkFileFailed(ctx, task.ID, 2, "boom")
	require.NoError(t, err)

	require.NoError(t, mngr.Enqueue(ctx, task.ID))

	disp.mu.Lock()
	defer disp.mu.Unlock()
	require.Len(t, disp.jobs, 1)
	require.Equal(t, 3, disp.jobs[0].FileIndex)

	stored, err := svc.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusQueued, stored.Files[3].Status)
}

func TestDownloadManagerDisableEnqueueSkipsSubmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, store := newTestServiceWithClock(t, time.Now().Add(2*time.Minute), time.Millisecond*10)
	defer func() { _ = store.Close() }()

	disp := &mockJobSubmitter{}
	mngr := &DownloadManager{pool: disp, svc: svc}

	task, err := svc.CreateTask(ctx, []string{"https://docs.google.com/never"})
	require.NoError(t, err)

	mngr.DisableEnqueue()
	require.NoError(t, mngr.Enqueue(ctx, task.ID))

	disp.mu.Lock()
	defer disp.mu.Unlock()
	require.Len(t, disp.jobs, 0)

	stored, err := svc.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, core.FileStatusWaiting, stored.Files[0].Status)
}
