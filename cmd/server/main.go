package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mauzec/web-downloader/internal/api"
	"github.com/mauzec/web-downloader/internal/config"
	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/service"
	"github.com/mauzec/web-downloader/internal/storage"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	configAppName = "app"
	configExt     = "env"
	configDir     = "config"
)

func newLogger() (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.MessageKey = "msg"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.OutputPaths = []string{"stdout", "app_log.log"}
	cfg.ErrorOutputPaths = []string{"stderr", "app_log.log"}
	return cfg.Build()
}

func main() {
	zapLogger, err := newLogger()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "can init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = zapLogger.Sync()
	}()

	logger := zapLogger.Named("server")

	logger.Info("running server", zap.Int("pid", os.Getpid()))

	cfg, err := readConfig()
	if err != nil || cfg == nil {
		logger.Fatal("cnat read config, check file", zap.Error(err), zap.String("name", configAppName))
	}

	if err := os.MkdirAll(cfg.SecDir, 0o755); err != nil {
		logger.Fatal("cant create sec dirs", zap.Error(err), zap.String("dir", cfg.SecDir))
	}
	if err := os.MkdirAll(cfg.FilesDir, 0o755); err != nil {
		logger.Fatal("cant create files dir", zap.Error(err), zap.String("dir", cfg.FilesDir))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	restartCh := make(chan os.Signal, 1)
	signal.Notify(restartCh, syscall.SIGHUP)
	defer signal.Stop(restartCh)

	h := &holder{}
	compLogger := logger.Named("comp")
	c, err := newAppComponent(ctx, cfg, compLogger)
	if err != nil {
		logger.Fatal("cant create app component", zap.Error(err))
	}
	h.set(c)

	if err := c.mngr.EnqueueAll(ctx); err != nil {
		logger.Warn("cant enqueue tasks", zap.Error(err))
	}

	srv, err := api.NewServer(&api.ServerOptions{
		TaskService: &taskServiceProxy{holder: h},
		Downloader:  &downloaderProxy{holder: h},
		Logger:      logger,

		Addr: cfg.ServerAddr,
	})
	if err != nil {
		logger.Fatal("cant create api server", zap.Error(err))
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting server", zap.String("addr", cfg.ServerAddr))
		if err := srv.Run(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				return
			}
			errCh <- err
		}
	}()

	sdCtx := ctx
	for {
		select {
		case <-sdCtx.Done():
			logger.Info("shutdown signal received")
			goto shutdown
		case <-restartCh:
			if err := restartComponent(ctx, h, cfg, compLogger); err != nil {
				logger.Error("restart failed", zap.Error(err))
			}
		case err := <-errCh:
			logger.Error("server failed", zap.Error(err))

		}
	}

shutdown:
	comp := h.get()
	if comp != nil {
		comp.stopNewDownloads(logger, "shutownw")
	}

	offCtx, offCanc := context.WithTimeout(context.Background(), 30*time.Second)
	defer offCanc()
	if err := srv.Shutdown(offCtx); err != nil {
		logger.Error("cant shutdown server", zap.Error(err))
	}
	if comp != nil {
		comp.stopSnapshot()
		comp.flushSnapshot(cfg.SnapshotTimeout, compLogger)
		comp.closeStore(compLogger)
	}
	logger.Info("shutdown done")
}

type taskStorage interface {
	storage.TaskStore
	service.SnapshotWritter
}
type appComponent struct {
	svc   *service.TaskService
	mngr  *service.DownloadManager
	store taskStorage

	workCanc context.CancelFunc

	snapStop    chan struct{}
	snapWG      sync.WaitGroup
	snapEnabled bool
}

func newAppComponent(ctx context.Context, cfg *config.AppConfig, logger *zap.Logger) (*appComponent, error) {
	wCtx, wCanc := context.WithCancel(ctx)
	svc, mngr, store, snapEnabled, err := setup(wCtx, cfg, logger)
	if err != nil {
		wCanc()
		if store != nil {
			_ = store.Close()
		}
		return nil, err
	}

	c := &appComponent{
		svc:         svc,
		mngr:        mngr,
		store:       store,
		workCanc:    wCanc,
		snapEnabled: snapEnabled,
	}
	if snapEnabled {
		c.snapStop = make(chan struct{})
		c.snapWG.Add(1)

		go func() {
			defer c.snapWG.Done()
			ticker := time.NewTicker(cfg.SnapshotInterval)
			defer ticker.Stop()

			for {

				select {
				case <-ticker.C:
					logger.Info("starting period snapshotting")
					snapCtx, canc := context.WithTimeout(context.Background(), cfg.SnapshotTimeout)
					if err := svc.FlushSnapshot(snapCtx); err != nil {
						logger.Error("period snapshot failed", zap.Error(err))
					}
					canc()
					logger.Info("period snapshotting done")
				case <-c.snapStop:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	} else {
		logger.Info("snapshotting disabled", zap.String("storage_mode", cfg.StorageMode))

	}
	return c, nil
}

func (c *appComponent) stopNewDownloads(logger *zap.Logger, why string) {
	logger.Info("disabling new downloads", zap.String("why", why))
	c.mngr.DisableEnqueue()
	logger.Info("cancel download workers", zap.String("why", why))
	c.workCanc()
	logger.Info("wait for active downloads to finish", zap.String("why", why))
	c.mngr.Close()
}
func (c *appComponent) stopSnapshot() {
	if !c.snapEnabled {
		return
	}

	if c.snapStop != nil {
		close(c.snapStop)
		c.snapStop = nil
	}
	c.snapWG.Wait()
}

func (c *appComponent) flushSnapshot(timeout time.Duration, logger *zap.Logger) {
	if c.svc == nil {
		return
	}
	fCtx, fCanc := context.WithTimeout(context.Background(), timeout)
	defer fCanc()
	if err := c.svc.FlushSnapshot(fCtx); err != nil {
		logger.Error("cant flush snapshot", zap.Error(err))
	}
}
func (c *appComponent) closeStore(logger *zap.Logger) {
	if c.store == nil {
		return
	}
	if err := c.store.Close(); err != nil {
		logger.Error("cant close store", zap.Error(err))
	}
	c.store = nil
}

type holder struct {
	mu   sync.RWMutex
	comp *appComponent
}

func (h *holder) set(c *appComponent) {
	h.mu.Lock()
	h.comp = c
	h.mu.Unlock()
}
func (h *holder) get() *appComponent {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.comp
	h.comp = nil
	return c
}
func (h *holder) withService(f func(*service.TaskService) error) error {
	h.mu.RLock()
	c := h.comp
	if c == nil {
		h.mu.RUnlock()
		return errors.New("service not available")
	}
	defer h.mu.RUnlock()
	return f(c.svc)
}
func (h *holder) withDownloader(f func(*service.DownloadManager) error) error {
	h.mu.RLock()
	c := h.comp
	if c == nil {
		h.mu.RUnlock()
		return errors.New("downloader not available")
	}
	defer h.mu.RUnlock()
	return f(c.mngr)
}

type taskServiceProxy struct {
	holder *holder
}

func (p *taskServiceProxy) CreateTask(ctx context.Context, urls []string) (*core.Task, error) {
	res := &core.Task{}
	err := p.holder.withService(func(ts *service.TaskService) error {
		var thisErr error
		res, thisErr = ts.CreateTask(ctx, urls)
		return thisErr
	})
	return res, err
}
func (p *taskServiceProxy) GetTask(ctx context.Context, id string) (*core.Task, error) {
	res := &core.Task{}
	err := p.holder.withService(func(ts *service.TaskService) error {
		var thisErr error
		res, thisErr = ts.GetTask(ctx, id)
		return thisErr
	})
	return res, err
}
func (p *taskServiceProxy) SortedTasks(ctx context.Context) ([]*core.Task, error) {
	res := []*core.Task{}
	err := p.holder.withService(func(ts *service.TaskService) error {
		var thisErr error
		res, thisErr = ts.SortedTasks(ctx)
		return thisErr
	})
	return res, err
}

type downloaderProxy struct {
	holder *holder
}

func (p *downloaderProxy) Enqueue(ctx context.Context, taskID string) error {
	return p.holder.withDownloader(func(dm *service.DownloadManager) error {
		return dm.Enqueue(ctx, taskID)
	})
}
func (p *downloaderProxy) EnqueueAll(ctx context.Context) error {
	return p.holder.withDownloader(func(dm *service.DownloadManager) error {
		return dm.EnqueueAll(ctx)
	})
}
func (p *downloaderProxy) Close() {
	_ = p.holder.withDownloader(func(dm *service.DownloadManager) error {
		dm.Close()
		return nil
	})
}

func readConfig() (*config.AppConfig, error) {
	return config.LoadAppConfig(configAppName, configExt, configDir)
}

func setup(ctx context.Context, cfg *config.AppConfig, logger *zap.Logger) (*service.TaskService, *service.DownloadManager, taskStorage, bool, error) {
	storageMode := strings.ToLower(cfg.StorageMode)

	var store taskStorage
	var err error

	switch storageMode {
	case "memory":
		store, err = storage.NewFileTaskStore(
			filepath.Join(cfg.SecDir, "snap.json"),
			filepath.Join(cfg.SecDir, "wal.log"),
		)
	case "bbolt":
		store, err = storage.NewBoltTaskStore(
			filepath.Join(cfg.SecDir, "tasks.db"),
		)
	default:
		return nil, nil, nil, false, errors.New("unknown storage mode")
	}
	if err != nil {
		return nil, nil, nil, false, err
	}

	svc, err := service.NewTaskService(
		context.Background(),
		store,
		store,
		service.NewRandomIDGenerator("webdl-t-"),
		time.Now,
	)
	if err != nil {
		_ = store.Close()
		return nil, nil, nil, false, err
	}

	mngr, err := service.NewDownloadManager(ctx, svc, &service.DownloadManagerOptions{
		HTTPClient:      &http.Client{Timeout: cfg.DownloadTimeout},
		Dir:             cfg.FilesDir,
		MaxCPU:          cfg.MaxCPU,
		QueueSize:       cfg.DownloadQueueSize,
		DownloadTimeout: cfg.DownloadTimeout,
		UserAgent:       cfg.UserAgent,

		MaxAttempts:     cfg.RetryDownloadFileMaxRetries,
		BackoffDuration: cfg.RetryDownloadFileWait,

		RetryEnqueueInterval: cfg.RetryAllWhenQueueFullWait,
	})
	if err != nil {
		return nil, nil, nil, false, err
	}
	return svc, mngr, store, storageMode == "memory", nil
}

func restartComponent(ctx context.Context, h *holder, cfg *config.AppConfig, logger *zap.Logger) error {
	logger.Info("restart required")
	h.mu.RLock()
	curr := h.comp
	h.mu.RUnlock()
	if curr == nil {
		return errors.New("component not init")
	}
	curr.stopNewDownloads(logger, "restart")
	h.mu.Lock()
	defer h.mu.Unlock()

	last := h.comp
	if last == nil {
		return errors.New("component not init while restart")
	}
	last.stopSnapshot()
	last.flushSnapshot(cfg.SnapshotTimeout, logger)
	last.closeStore(logger)

	newer, err := newAppComponent(ctx, cfg, logger)
	if err != nil {
		return err
	}
	h.comp = newer
	if err := newer.mngr.EnqueueAll(ctx); err != nil {
		logger.Warn("cant enqueue tasks", zap.Error(err))
	}
	logger.Info("restart done")
	return nil
}
