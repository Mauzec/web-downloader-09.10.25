package service

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/worker"
)

type jobSubmitter interface {
	Submit(ctx context.Context, job worker.Job) error
	Stop()
}

type DownloadManager struct {
	pool jobSubmitter
	svc  *TaskService

	// fields for retry pending tasks
	retryEnqueueInterval time.Duration
	retryTicker          *time.Ticker
	retryStop            chan struct{}
	retryOnce            sync.Once
	retryWG              sync.WaitGroup

	// to disable enqueue new jobs
	enqueueDisabled      atomic.Bool
}
type DownloadManagerOptions struct {
	HTTPClient      *http.Client  `validate:"required"`
	Dir             string        `validate:"required"`
	MaxCPU          int           `validate:"min=1"`
	QueueSize       int           `validate:"min=1"`
	DownloadTimeout time.Duration `validate:"required"`
	UserAgent       string        `validate:"required"`

	// for retry download the task

	MaxAttempts     int           `validate:"required"`
	BackoffDuration time.Duration `validate:"required"`

	// for pending tasks retry

	RetryEnqueueInterval time.Duration `validate:"required"`
}

// NewDownloadManager starts download manager with worker pool
func NewDownloadManager(ctx context.Context, svc *TaskService, opts *DownloadManagerOptions) (*DownloadManager, error) {
	if err := validator.New().Struct(opts); err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if svc == nil {
		return nil, errors.New("download mng: required task service")
	}

	downloader, err := worker.NewDownloader(ctx, svc, &worker.DownloaderConfig{
		Client:          opts.HTTPClient,
		Dir:             opts.Dir,
		Timeout:         opts.DownloadTimeout,
		UserAgent:       opts.UserAgent,
		MaxAttempts:     opts.MaxAttempts,
		BackoffDuration: opts.BackoffDuration,
	})
	if err != nil {
		return nil, err
	}

	pool, err := worker.NewPool(opts.MaxCPU, downloader, opts.QueueSize)
	if err != nil {
		return nil, err
	}
	if err := pool.Start(); err != nil {
		return nil, err
	}

	dm := &DownloadManager{
		svc:                  svc,
		pool:                 pool,
		retryEnqueueInterval: opts.RetryEnqueueInterval,
	}

	dm.retryTicker = time.NewTicker(opts.RetryEnqueueInterval)
	dm.retryStop = make(chan struct{})
	dm.retryWG.Add(1)
	go dm.retryLoop(ctx)

	return dm, nil
}

// Close stops the worker pool.
func (dm *DownloadManager) Close() {
	if dm == nil {
		return
	}
	dm.DisableEnqueue()
	dm.retryOnce.Do(func() {
		if dm.retryTicker != nil {
			dm.retryTicker.Stop()
		}
		if dm.retryStop != nil {
			close(dm.retryStop)
		}
	})
	dm.retryWG.Wait()
	if dm.pool != nil {
		dm.pool.Stop()
	}
}

// DisableEnqueue stops accepting new jobs.
func (dm *DownloadManager) DisableEnqueue() {
	dm.enqueueDisabled.Store(true)
}

// Enqueue queues all files of a task that are not completed or in error state.
func (dm *DownloadManager) Enqueue(ctx context.Context, taskID string) error {
	if dm.enqueueDisabled.Load() {
		return nil
	}
	if ctx == nil {
		return errors.New("download mgr: required context")
	}

	task, err := dm.svc.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	for i, f := range task.Files {
		if f == nil {
			continue
		}
		if f.Status == core.FileStatusError || f.Status == core.FileStatusCompleted {
			continue
		}
		if f.Status == core.FileStatusQueued || f.Status == core.FileStatusDownloading {
			continue
		}

		job := worker.Job{URL: f.URL, TaskID: taskID, FileIndex: i}
		if err := dm.pool.Submit(ctx, job); err != nil {
			if errors.Is(err, worker.ErrPoolFull) {
				return err
			}
			return err
		}
		if _, err := dm.svc.MarkFileQueued(ctx, taskID, i); err != nil {
			return err
		}
	}
	return nil
}

// EnqueuePending is [Enqueue], but for all tasks.
func (dm *DownloadManager) EnqueueAll(ctx context.Context) error {
	if dm.enqueueDisabled.Load() {
		return nil
	}
	if ctx == nil {
		return errors.New("download mgr: required context")
	}
	tasks, err := dm.svc.SortedTasks(ctx)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if err := dm.Enqueue(ctx, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// retryLoop retries to enqueue pending tasks sometimes.
func (dm *DownloadManager) retryLoop(ctx context.Context) {
	defer dm.retryWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-dm.retryStop:
			return
		case <-dm.retryTicker.C:
			if dm.enqueueDisabled.Load() {
				continue
			}
			retryCtx, cancel := context.WithTimeout(context.Background(), dm.retryEnqueueInterval)
			err := dm.EnqueueAll(retryCtx)
			cancel()
			if err != nil {
				if errors.Is(err, worker.ErrPoolFull) || errors.Is(err, context.Canceled) {
					continue
				}
				if errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				log.Printf("download manager: cant retry enqueue: %v", err)
			}
		}
	}
}
