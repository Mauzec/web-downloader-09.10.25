package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/stretchr/testify/require"
)

type stubUpdater struct {
	started   []int
	completed []completedStruct
	failed    []failedStruct

	mu sync.Mutex
}

type completedStruct struct {
	taskID    string
	fileIndex int
	filename  string
	size      int64
}

type failedStruct struct {
	taskID    string
	fileIndex int
	why       string
}

func (s *stubUpdater) MarkFileStarted(
	ctx context.Context,
	taskID string,
	fileIndex int,
) (*core.Task, error) {
	s.mu.Lock()
	s.started = append(s.started, fileIndex)
	s.mu.Unlock()
	return nil, nil
}

func (s *stubUpdater) MarkFileCompleted(
	ctx context.Context,
	taskID string,
	fileIndex int,
	filename string,
	size int64,
) (*core.Task, error) {
	s.mu.Lock()
	s.completed = append(s.completed, completedStruct{
		taskID, fileIndex, filename, size},
	)
	s.mu.Unlock()
	return nil, nil
}

func (s *stubUpdater) MarkFileFailed(
	ctx context.Context,
	taskID string,
	fileIndex int,
	why string,
) (*core.Task, error) {
	s.mu.Lock()
	s.failed = append(s.failed, failedStruct{
		taskID, fileIndex, why},
	)
	s.mu.Unlock()
	return nil, nil
}

type errUpdater struct{}

func (e *errUpdater) MarkFileStarted(ctx context.Context, taskID string, fileIndex int) (*core.Task, error) {
	return nil, fmt.Errorf("srt-hey")
}

func (e *errUpdater) MarkFileCompleted(ctx context.Context, taskID string, fileIndex int, filename string, size int64) (*core.Task, error) {
	return nil, nil
}

func (e *errUpdater) MarkFileFailed(ctx context.Context, taskID string, fileIndex int, why string) (*core.Task, error) {
	return nil, nil
}

func TestDownloaderOK(t *testing.T) {
	t.Parallel()

	msg := "wake up, samurai"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(msg))
	}))

	t.Cleanup(server.Close)

	updater := &stubUpdater{}
	dir := t.TempDir()
	cfg := DownloaderConfig{Dir: dir, Timeout: time.Second, Client: server.Client()}

	dlr, err := NewDownloader(context.Background(), updater, &cfg)
	require.NoError(t, err)
	job := Job{TaskID: "simon", FileIndex: 0, URL: server.URL + "/simon.toml"}
	require.NoError(t, dlr.Handle(job))
	require.Len(t, updater.started, 1)
	require.Len(t, updater.completed, 1)
	require.Empty(t, updater.failed)

	c := updater.completed[0]
	require.Equal(t, "simon.toml", c.filename)
	require.Equal(t, int64(len(msg)), c.size)

	path := filepath.Join(dir, "simon", "simon.toml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, msg, string(data))
}

func TestDownloaderStatusError(t *testing.T) {
	t.Parallel()

	msg := "say hey"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, msg, http.StatusInternalServerError)
	}))

	t.Cleanup(server.Close)

	updater := &stubUpdater{}
	dir := t.TempDir()
	cfg := DownloaderConfig{Dir: dir, Timeout: time.Second, Client: server.Client()}
	dlr, err := NewDownloader(context.Background(), updater, &cfg)
	require.NoError(t, err)
	job := Job{TaskID: "bad simon", FileIndex: 1, URL: server.URL}
	require.Error(t, dlr.Handle(job))
	require.Len(t, updater.started, 1)
	require.Len(t, updater.failed, 1)
	require.Contains(t, updater.failed[0].why, "500")
}

func TestDownloaderRetries(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 2 {
			http.Error(w, "try again", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	updater := &stubUpdater{}
	dir := t.TempDir()
	cfg := DownloaderConfig{
		Dir:             dir,
		Timeout:         time.Second,
		Client:          server.Client(),
		MaxAttempts:     2,
		BackoffDuration: 10 * time.Millisecond,
	}

	dlr, err := NewDownloader(context.Background(), updater, &cfg)
	require.NoError(t, err)

	job := Job{TaskID: "simon", FileIndex: 0, URL: server.URL + "/retry"}
	require.NoError(t, dlr.Handle(job))
	require.Len(t, updater.started, 1)
	require.Len(t, updater.completed, 1)
	require.Empty(t, updater.failed)
	require.GreaterOrEqual(t, atomic.LoadInt32(&attempts), int32(2))
}

func TestDownloaderTimeout(t *testing.T) {
	t.Parallel()

	msg := "you are who"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(msg))
	}))

	t.Cleanup(server.Close)

	updater := &stubUpdater{}
	dir := t.TempDir()
	cfg := DownloaderConfig{Dir: dir, Timeout: 25 * time.Millisecond, Client: server.Client()}
	dlr, err := NewDownloader(context.Background(), updater, &cfg)
	require.NoError(t, err)
	job := Job{TaskID: "simon fell", FileIndex: 1, URL: server.URL}
	require.Error(t, dlr.Handle(job))
	require.Len(t, updater.started, 1)
	require.Len(t, updater.failed, 1)
	require.Contains(t, updater.failed[0].why, "context deadline exceeded")
}

func TestDownloaderFailOnRequest(t *testing.T) {
	t.Parallel()

	updater := &stubUpdater{}
	dir := t.TempDir()
	cfg := DownloaderConfig{Dir: dir, Timeout: time.Second,
		Client: &http.Client{Timeout: time.Second},
	}
	dlr, err := NewDownloader(context.Background(), updater, &cfg)
	require.NoError(t, err)
	job := Job{TaskID: "simon?", FileIndex: 3, URL: "http://bad^url"}
	require.Error(t, dlr.Handle(job))
	require.Len(t, updater.started, 1)
	require.Len(t, updater.failed, 1)
	require.Contains(t, updater.failed[0].why, "create request")
}

func TestDownloaderFailOnUpdate(t *testing.T) {
	t.Parallel()

	updater := &errUpdater{}
	dir := t.TempDir()
	cfg := DownloaderConfig{Dir: dir, Timeout: time.Second,
		Client: &http.Client{Timeout: time.Second},
	}
	dlr, err := NewDownloader(context.Background(), updater, &cfg)
	require.NoError(t, err)

	job := Job{TaskID: "simon", FileIndex: 0, URL: "http://docs.google.com/smn"}
	err = dlr.Handle(job)
	require.Error(t, err)
	require.Contains(t, err.Error(), "started")
}
