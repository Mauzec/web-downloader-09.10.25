package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
)

type TaskUpdater interface {
	MarkFileStarted(ctx context.Context,
		taskID string,
		fileIndex int,
	) (*core.Task, error)
	MarkFileCompleted(
		ctx context.Context,
		taskID string,
		fileIndex int,
		filename string,
		size int64,
	) (*core.Task, error)
	MarkFileFailed(
		ctx context.Context,
		taskID string,
		fileIndex int,
		why string,

	) (*core.Task, error)
}

// Handler handles jobs.
type Handler interface {
	Handle(job Job) error
}

type DownloaderConfig struct {
	Client *http.Client

	Dir             string
	Timeout         time.Duration
	UserAgent       string
	MaxAttempts     int
	BackoffDuration time.Duration
}

// Downloader http-downloads files and updates task status.
type Downloader struct {
	client *http.Client

	ctx context.Context

	updater         TaskUpdater
	dir             string
	timeout         time.Duration
	userAgent       string
	maxAttempts     int
	backoffDuration time.Duration
}

func NewDownloader(ctx context.Context, updater TaskUpdater, cfg *DownloaderConfig) (*Downloader, error) {
	if updater == nil {
		return nil, errors.New("downloader:required task updater")
	}
	if ctx == nil {
		return nil, errors.New("downloader:required ctx")
	}
	if cfg == nil {
		return nil, errors.New("downloader: required config")
	}

	return &Downloader{
		client:          cfg.Client,
		ctx:             ctx,
		updater:         updater,
		dir:             cfg.Dir,
		timeout:         cfg.Timeout,
		userAgent:       cfg.UserAgent,
		maxAttempts:     cfg.MaxAttempts,
		backoffDuration: cfg.BackoffDuration,
	}, nil
}

func (dlr *Downloader) Handle(job Job) error {
	baseCtx := dlr.ctx
	startCtx, cancelStart := context.WithTimeout(baseCtx, dlr.timeout)
	defer cancelStart()

	if _, err := dlr.updater.MarkFileStarted(startCtx, job.TaskID, job.FileIndex); err != nil {
		return fmt.Errorf("downloader: mark started: %w", err)
	}

	attempts := dlr.maxAttempts + 1
	var lastErr error

attemptLoop:
	for attempt := range attempts {
		if baseCtx != nil && baseCtx.Err() != nil {
			lastErr = baseCtx.Err()
			break
		}

		attemptCtx, cancelAttempt := context.WithTimeout(baseCtx, dlr.timeout)
		filename, size, err := dlr.downloadOnce(attemptCtx, job)
		cancelAttempt()

		if err == nil {
			statusCtx, cancel := dlr.statusContext(baseCtx)
			if _, err := dlr.updater.MarkFileCompleted(
				statusCtx,
				job.TaskID,
				job.FileIndex,
				filename,
				size,
			); err != nil {
				lastErr = fmt.Errorf("downloader: mark completed: %w", err)
				cancel()
				break
			}
			cancel()
			return nil
		}

		lastErr = err
		if attempt == attempts-1 {
			break
		}

		t := time.NewTimer(dlr.backoffDuration)
		select {
		case <-t.C:
			t.Stop()
		case <-baseCtx.Done():
			t.Stop()
			lastErr = baseCtx.Err()
			break attemptLoop
		}

	}

	_ = dlr.markFailed(baseCtx, job, lastErr)
	if lastErr == nil {
		lastErr = errors.New("downloader: unknown failure")
	}
	return lastErr
}

func (dlr *Downloader) downloadOnce(ctx context.Context, job Job) (string, int64, error) {
	if ctx == nil {
		return "", 0, errors.New("downloader: missing context")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.URL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("downloader: create request %w", err)
	}

	req.Header.Set("User-Agent", dlr.userAgent)
	resp, err := dlr.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("downloader: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", 0, fmt.Errorf("downloader: bad response status %d", resp.StatusCode)
	}

	filename := dlr.extractFilename(job, resp)
	size, err := dlr.writeFile(ctx, job, filename, resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("downloader: write: %w", err)
	}

	return filename, size, nil
}

func (dlr *Downloader) markFailed(ctx context.Context, job Job, why error) error {
	if why == nil {
		why = errors.New("unknown error")
	}
	msg := why.Error()
	if len(msg) > 256 {
		msg = msg[:256]
	}
	statusCtx, cancel := dlr.statusContext(ctx)
	defer cancel()

	_, err := dlr.updater.MarkFileFailed(statusCtx, job.TaskID, job.FileIndex, msg)
	return err
}

const contentDispositionHeader = "Content-Disposition"

func (dlr *Downloader) extractFilename(job Job, resp *http.Response) string {
	if resp != nil {
		if cdhValue := resp.Header.Get(contentDispositionHeader); cdhValue != "" {
			if _, ps, err := mime.ParseMediaType(cdhValue); err == nil {
				if res := normalizeFilename(ps["filename"]); res != "" {
					return res
				}
			}
		}
	}

	if p, err := url.Parse(job.URL); err == nil {
		if res := normalizeFilename(path.Base(p.Path)); res != "" {
			return res
		}
	}
	return "f-" + strconv.Itoa(job.FileIndex)
}
func normalizeFilename(name string) string {
	res := strings.TrimSpace(name)
	if res == "" {
		return ""
	}
	res = filepath.Base(res)
	res = strings.Trim(res, ". ")
	if res == "" || res == string(os.PathSeparator) {
		return ""
	}
	return res
}

type contextReader struct {
	r   io.Reader
	ctx context.Context
}

func (dlr *Downloader) writeFile(
	ctx context.Context,
	job Job,
	filename string,
	r io.Reader,
) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	dir := filepath.Join(dlr.dir, job.TaskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	normPath := normalizeFilename(filename)
	if normPath == "" {
		normPath = "f_" + strconv.Itoa(job.FileIndex)
	}
	normPath = filepath.Join(dir, normPath)
	tmpPath := normPath + ".tmp"

	f, err := os.OpenFile(tmpPath,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644,
	)
	if err != nil {
		return 0, fmt.Errorf("open tmp: %w", err)
	}
	//

	reader := &contextReader{r, ctx}
	n, copyErr := io.Copy(f, reader)
	syncErr := f.Sync()
	closeErr := f.Close()

	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return n, fmt.Errorf("copybody: %w", copyErr)
	} else if err := ctx.Err(); err != nil {
		_ = os.Remove(tmpPath)
		return n, err
	} else if syncErr != nil {
		_ = os.Remove(tmpPath)
		return n, fmt.Errorf("sync file: %w", syncErr)
	} else if closeErr != nil {
		_ = os.Remove(tmpPath)
		return n, fmt.Errorf("closing file: %w", closeErr)
	} else if err := ctx.Err(); err != nil {
		_ = os.Remove(tmpPath)
		return n, err
	} else if err := os.Rename(tmpPath, normPath); err != nil {
		_ = os.Remove(tmpPath)
		return n, fmt.Errorf("rename tmp file: %w", err)
	}
	return n, nil
}

func (dlr *Downloader) statusContext(_ context.Context) (context.Context, context.CancelFunc) {
	base := dlr.ctx
	if base == nil || base.Err() != nil {
		base = context.Background()
	}

	return context.WithTimeout(base, dlr.timeout)
}

func (cr *contextReader) Read(p []byte) (int, error) {
	select {
	case <-cr.ctx.Done():
		return 0, cr.ctx.Err()
	default:
		return cr.r.Read(p)
	}
}
