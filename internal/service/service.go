package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage"
	"github.com/mauzec/web-downloader/internal/storage/wal"
	"github.com/mauzec/web-downloader/internal/utils"
)

type SnapshotWritter interface {
	// FlushSnapshot persists tasks as a new snapshot and wait until it is stored.
	FlushSnapshot(ctx context.Context) error
}

type boltTaskAccessor interface {
	GetTaskByID(ctx context.Context, id string) (*core.Task, error)
	ListTasks(ctx context.Context) ([]*core.Task, error)
}

const walVersion = 1

type TaskService struct {
	store    storage.TaskStore
	snapshot SnapshotWritter

	idGen IDGenerator

	tasks    map[string]*core.Task
	useCache bool
	bolt     boltTaskAccessor

	mu  sync.RWMutex
	now func() time.Time
}

func NewTaskService(
	ctx context.Context,
	store storage.TaskStore,
	snapshot SnapshotWritter,
	idGen IDGenerator,
	now func() time.Time,
) (*TaskService, error) {
	const op = "service.NewTaskService"
	if store == nil {
		return nil, core.NewAppErrorBuilder(core.ErrorCodeInternal).
			Message("task store required").
			SafeToShow(false).
			Oper(op).
			Build()
	}
	if idGen == nil {
		return nil, core.NewAppErrorBuilder(core.ErrorCodeInternal).
			Message("id gen required").
			SafeToShow(false).
			Oper(op).
			Build()
	}
	if now == nil {
		now = time.Now
	}
	useCache := true
	var boltAcc boltTaskAccessor
	if acc, ok := store.(boltTaskAccessor); ok {
		useCache = false
		boltAcc = acc
	}

	var tasks map[string]*core.Task
	if useCache {
		loadedTasks, err := store.LoadAll(ctx)
		if err != nil {
			return nil, core.NewTaskInternalError("load tasks", err, op)
		}
		tasks = make(map[string]*core.Task, len(loadedTasks))
		for _, t := range loadedTasks {
			tasks[t.ID] = t.CloneTask()
		}
	}

	return &TaskService{
		store:    store,
		snapshot: snapshot,
		idGen:    idGen,
		tasks:    tasks,
		useCache: useCache,
		bolt:     boltAcc,
		now:      now,
	}, nil
}

func (ts *TaskService) CreateTask(ctx context.Context, urls []string) (*core.Task, error) {
	const op = "service.TaskService.CreateTask"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "ctx error", err)
	}

	curls, err := normalizeURLs(urls)
	if err != nil {
		return nil, err
	}

	id, genErr := ts.idGen.NewID()
	if genErr != nil {
		return nil, internalError(op, "gen id error", genErr)
	}

	now := ts.now().UTC()
	nowPtr := utils.TimePtr(now)
	t := core.NewTask(id, nowPtr, curls...)
	t.UpdatedAt = utils.TimePtr(now)

	ev, evErr := createEvent(id,
		wal.EventTaskCreated,
		now,
		wal.TaskCreatedPayload{Task: t},
	)
	if evErr != nil {
		return nil, internalError(op, "encode task_created", evErr)
	}
	if err := ts.store.CreateTask(ctx, t.CloneTask(), ev); err != nil {
		return nil, internalError(op, "create task", err)
	}

	if ts.useCache {
		ts.mu.Lock()
		ts.tasks[id] = t.CloneTask()
		ts.mu.Unlock()
	}

	return t.CloneTask(), nil
}

// GetTask returns only one task by id.
func (ts *TaskService) GetTask(ctx context.Context, taskID string) (*core.Task, error) {
	const op = "service.TaskService.GetTask"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "ctx error", err)
	}

	if ts.useCache {
		ts.mu.RLock()
		t, ok := ts.tasks[taskID]
		ts.mu.RUnlock()
		if !ok {
			return nil, core.NewTaskNotFoundError(taskID, op)
		}
		return t.CloneTask(), nil
	}

	if ts.bolt == nil {
		return nil, core.NewTaskNotFoundError(taskID, op)
	}

	t, err := ts.bolt.GetTaskByID(ctx, taskID)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}
	return t.CloneTask(), nil
}

// SortedTasks returns all tasks sorted by created time.
func (ts *TaskService) SortedTasks(ctx context.Context) ([]*core.Task, error) {
	const op = "service.TaskService.SortedTasks"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "ctx error", err)
	}

	if ts.useCache {
		ts.mu.RLock()
		res := make([]*core.Task, 0, len(ts.tasks))
		for _, t := range ts.tasks {
			res = append(res, t.CloneTask())
		}
		ts.mu.RUnlock()

		core.SortTasks(res)
		return res, nil
	}

	if ts.bolt == nil {
		res, err := ts.store.LoadAll(ctx)
		if err != nil {
			return nil, tryAsAppError(err, op)
		}
		return res, nil
	}

	res, err := ts.bolt.ListTasks(ctx)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}
	return res, nil
}

func (ts *TaskService) MarkFileStarted(
	ctx context.Context,
	taskID string,
	fileIndex int,
) (*core.Task, error) {
	const op = "service.TaskService.MarkFileStarted"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "context error", err)
	}

	if ts.useCache {
		t, finish, err := ts.cloneForUpdate(taskID, fileIndex, op)
		if err != nil {
			return nil, err
		}
		didFinish := false
		defer func() {
			if !didFinish {
				finish(nil)
			}
		}()

		f := t.Files[fileIndex]
		if f.Status == core.FileStatusCompleted {
			return nil, validationError(op, "file already completed")
		}

		now := ts.now().UTC()

		f.Status = core.FileStatusDownloading
		f.Error = ""
		f.StartedAt = utils.TimePtr(now)

		prevStatus := t.Status
		t.UpdatedAt = utils.TimePtr(now)
		t.UpdateStatus(&now)

		evs, err := ts.buildFileEvents(
			taskID,
			fileIndex,
			f,
			t.Status,
			prevStatus,
			now,
			buildEventOptions{
				includeMetadata: true,
			},
		)
		if err != nil {
			return nil, tryAsAppError(err, op)
		}

		if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
			return nil, internalError(op, "task update", err)
		}

		finish(t)
		didFinish = true

		return t.CloneTask(), nil
	}

	t, err := ts.loadTaskForUpdateNoCache(ctx, taskID, fileIndex, op)
	if err != nil {
		return nil, err
	}

	f := t.Files[fileIndex]
	if f.Status == core.FileStatusCompleted {
		return nil, validationError(op, "file already completed")
	}

	now := ts.now().UTC()

	f.Status = core.FileStatusDownloading
	f.Error = ""
	f.StartedAt = utils.TimePtr(now)

	prevStatus := t.Status
	t.UpdatedAt = utils.TimePtr(now)
	t.UpdateStatus(&now)

	evs, err := ts.buildFileEvents(
		taskID,
		fileIndex,
		f,
		t.Status,
		prevStatus,
		now,
		buildEventOptions{
			includeMetadata: true,
		},
	)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}

	if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
		return nil, internalError(op, "task update", err)
	}

	return t.CloneTask(), nil
}

func (ts *TaskService) MarkFileQueued(
	ctx context.Context,
	taskID string,
	fileIndex int,
) (*core.Task, error) {
	const op = "service.TaskService.MarkFileQueued"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "context error", err)
	}

	if ts.useCache {
		t, finish, err := ts.cloneForUpdate(taskID, fileIndex, op)
		if err != nil {
			return nil, err
		}
		didFinish := false
		defer func() {
			if !didFinish {
				finish(nil)
			}
		}()

		f := t.Files[fileIndex]
		switch f.Status {
		case core.FileStatusCompleted, core.FileStatusError:
			finish(t)
			didFinish = true
			return t.CloneTask(), nil
		case core.FileStatusQueued, core.FileStatusDownloading:
			finish(t)
			didFinish = true
			return t.CloneTask(), nil
		}

		now := ts.now().UTC()

		f.Status = core.FileStatusQueued
		f.Error = ""

		prevStatus := t.Status
		t.UpdatedAt = utils.TimePtr(now)
		t.UpdateStatus(&now)

		evs, err := ts.buildFileEvents(
			taskID,
			fileIndex,
			f,
			t.Status,
			prevStatus,
			now,
			buildEventOptions{},
		)
		if err != nil {
			return nil, tryAsAppError(err, op)
		}

		if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
			return nil, internalError(op, "task update", err)
		}

		finish(t)
		didFinish = true
		return t.CloneTask(), nil
	}

	t, err := ts.loadTaskForUpdateNoCache(ctx, taskID, fileIndex, op)
	if err != nil {
		return nil, err
	}

	f := t.Files[fileIndex]
	switch f.Status {
	case core.FileStatusCompleted, core.FileStatusError:
		return t.CloneTask(), nil
	case core.FileStatusQueued, core.FileStatusDownloading:
		return t.CloneTask(), nil
	}

	now := ts.now().UTC()

	f.Status = core.FileStatusQueued
	f.Error = ""

	prevStatus := t.Status
	t.UpdatedAt = utils.TimePtr(now)
	t.UpdateStatus(&now)

	evs, err := ts.buildFileEvents(
		taskID,
		fileIndex,
		f,
		t.Status,
		prevStatus,
		now,
		buildEventOptions{},
	)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}

	if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
		return nil, internalError(op, "task update", err)
	}

	return t.CloneTask(), nil
}

func (ts *TaskService) MarkFileCompleted(
	ctx context.Context,
	taskID string,
	fileIndex int,
	filename string,
	size int64,
) (*core.Task, error) {
	const op = "service.TaskService.MarkFileCompleted"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "ctx error", err)
	}
	if size < 0 {
		return nil, validationError(op, "file size shoud be >= 0")
	}

	if ts.useCache {
		t, finish, err := ts.cloneForUpdate(taskID, fileIndex, op)
		if err != nil {
			return nil, err
		}
		didFinish := false
		defer func() {
			if !didFinish {
				finish(nil)
			}
		}()

		f := t.Files[fileIndex]
		if f.Status == core.FileStatusCompleted {
			return nil, validationError(op, "file already completed")
		}

		now := ts.now().UTC()
		f.Status = core.FileStatusCompleted
		f.CompletedAt = utils.TimePtr(now)
		if f.StartedAt == nil {
			f.StartedAt = utils.TimePtr(now)
		}

		f.Filename = strings.TrimSpace(filename)
		f.Size = size
		f.Error = ""

		prevStatus := t.Status
		t.UpdatedAt = utils.TimePtr(now)
		t.UpdateStatus(&now)

		evs, err := ts.buildFileEvents(
			taskID, fileIndex,
			f, t.Status, prevStatus,
			now,
			buildEventOptions{
				includeMetadata: true,
			},
		)
		if err != nil {
			return nil, tryAsAppError(err, op)
		}

		if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
			return nil, internalError(op, "task update", err)
		}

		finish(t)
		didFinish = true

		return t.CloneTask(), nil
	}

	t, err := ts.loadTaskForUpdateNoCache(ctx, taskID, fileIndex, op)
	if err != nil {
		return nil, err
	}

	f := t.Files[fileIndex]
	if f.Status == core.FileStatusCompleted {
		return nil, validationError(op, "file already completed")
	}

	now := ts.now().UTC()
	f.Status = core.FileStatusCompleted
	f.CompletedAt = utils.TimePtr(now)
	if f.StartedAt == nil {
		f.StartedAt = utils.TimePtr(now)
	}

	f.Filename = strings.TrimSpace(filename)
	f.Size = size
	f.Error = ""

	prevStatus := t.Status
	t.UpdatedAt = utils.TimePtr(now)
	t.UpdateStatus(&now)

	evs, err := ts.buildFileEvents(
		taskID, fileIndex,
		f, t.Status, prevStatus,
		now,
		buildEventOptions{
			includeMetadata: true,
		},
	)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}

	if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
		return nil, internalError(op, "task update", err)
	}

	return t.CloneTask(), nil
}

func (ts *TaskService) MarkFileFailed(
	ctx context.Context,
	taskID string,
	fileIndex int,
	msg string,
) (*core.Task, error) {
	const op = "service.TaskService.MarkFileFailed"

	if err := ctx.Err(); err != nil {
		return nil, internalError(op, "ctx error", err)
	}

	if ts.useCache {
		t, finish, err := ts.cloneForUpdate(taskID, fileIndex, op)
		if err != nil {
			return nil, err
		}
		didFinish := false
		defer func() {
			if !didFinish {
				finish(nil)
			}
		}()

		f := t.Files[fileIndex]
		if f.Status == core.FileStatusCompleted {
			return nil, validationError(op, "file completed, it cant be failed")
		}

		now := ts.now().UTC()
		f.Status = core.FileStatusError
		f.Error = strings.TrimSpace(msg)
		f.CompletedAt = utils.TimePtr(now)
		if f.StartedAt == nil {
			f.StartedAt = utils.TimePtr(now)
		}

		prevStatus := t.Status
		t.UpdatedAt = utils.TimePtr(now)
		t.UpdateStatus(&now)

		evs, err := ts.buildFileEvents(
			taskID, fileIndex,
			f,
			t.Status,
			prevStatus,
			now,
			buildEventOptions{
				includeMetadata: true,
			},
		)
		if err != nil {
			return nil, tryAsAppError(err, op)
		}

		if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
			return nil, internalError(op, "task update", err)
		}

		finish(t)
		didFinish = true

		return t.CloneTask(), nil
	}

	t, err := ts.loadTaskForUpdateNoCache(ctx, taskID, fileIndex, op)
	if err != nil {
		return nil, err
	}

	f := t.Files[fileIndex]
	if f.Status == core.FileStatusCompleted {
		return nil, validationError(op, "file completed, it cant be failed")
	}

	now := ts.now().UTC()
	f.Status = core.FileStatusError
	f.Error = strings.TrimSpace(msg)
	f.CompletedAt = utils.TimePtr(now)
	if f.StartedAt == nil {
		f.StartedAt = utils.TimePtr(now)
	}

	prevStatus := t.Status
	t.UpdatedAt = utils.TimePtr(now)
	t.UpdateStatus(&now)

	evs, err := ts.buildFileEvents(
		taskID, fileIndex,
		f,
		t.Status,
		prevStatus,
		now,
		buildEventOptions{
			includeMetadata: true,
		},
	)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}

	if err := ts.store.UpdateTask(ctx, t.CloneTask(), evs...); err != nil {
		return nil, internalError(op, "task update", err)
	}

	return t.CloneTask(), nil
}

func (ts *TaskService) FlushSnapshot(ctx context.Context) error {
	const op = "service.TaskService.FlushSnapshot"

	if ts.snapshot == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return internalError(op, "ctx error", err)
	}

	if err := ts.snapshot.FlushSnapshot(ctx); err != nil {
		return internalError(op, "flush snapshot", err)
	}
	return nil
}

type buildEventOptions struct {
	includeMetadata bool
}

func (ts *TaskService) buildFileEvents(
	taskID string,
	fileIndex int,
	file *core.File,
	newStatus core.TaskStatus, prevStatus core.TaskStatus,
	occurred time.Time,
	opts buildEventOptions,
) ([]wal.Event, error) {
	evs := make([]wal.Event, 0, 3)

	statusPayload := wal.FileStatusPayload{
		FileIndex: fileIndex,
		Status:    file.Status,
		Error:     file.Error,
		Size:      file.Size,
	}
	statusEvent, err := createEvent(
		taskID, wal.EventFileStatus,
		occurred, statusPayload,
	)
	if err != nil {
		return nil, fmt.Errorf("encode file status: %w", err)
	}
	evs = append(evs, statusEvent)

	if opts.includeMetadata {
		metaPayload := wal.FileMetaPayload{
			StartedAt:   file.StartedAt,
			CompletedAt: file.CompletedAt,
			FileIndex:   fileIndex,
			FileName:    file.Filename,
		}
		metaEvent, err := createEvent(
			taskID, wal.EventFileMeta,
			occurred, metaPayload,
		)
		if err != nil {
			return nil, fmt.Errorf("encode file meta: %w", err)
		}
		evs = append(evs, metaEvent)
	}

	if newStatus != prevStatus {
		taskEvent, err := createEvent(
			taskID, wal.EventTaskStatus,
			occurred, wal.TaskStatusPayload{Status: newStatus},
		)
		if err != nil {
			return nil, fmt.Errorf("encode task status: %w", err)
		}
		evs = append(evs, taskEvent)
	}

	return evs, nil
}

func (ts *TaskService) cloneForUpdate(
	taskID string,
	fileIndex int,
	op string,
) (*core.Task, func(*core.Task), error) {
	ts.mu.Lock()
	task, ok := ts.tasks[taskID]
	if !ok {
		ts.mu.Unlock()
		return nil, nil, core.NewTaskNotFoundError(taskID, op)
	}
	if fileIndex < 0 {
		ts.mu.Unlock()
		return nil, nil, validationError(op, "file index should be >= 0")
	}
	if fileIndex >= len(task.Files) {
		ts.mu.Unlock()
		return nil, nil, validationError(op, "file index >= len(files)")
	}
	clone := task.CloneTask()
	return clone, func(updated *core.Task) {
		if updated != nil {
			ts.tasks[taskID] = updated.CloneTask()
		}
		ts.mu.Unlock()
	}, nil
}

func (ts *TaskService) loadTaskForUpdateNoCache(
	ctx context.Context,
	taskID string,
	fileIndex int,
	op string,
) (*core.Task, error) {
	if ts.bolt == nil {
		return nil, internalError(op, "no bolt", errors.New("bolt accessor missing"))
	}
	task, err := ts.bolt.GetTaskByID(ctx, taskID)
	if err != nil {
		return nil, tryAsAppError(err, op)
	}
	if fileIndex < 0 {
		return nil, validationError(op, "file index should be >= 0")
	}
	if fileIndex >= len(task.Files) {
		return nil, validationError(op, "file index >= len(files)")
	}
	return task.CloneTask(), nil
}

func normalizeURLs(urls []string) ([]string, *core.AppError) {
	const op = "service.normalizeURLs"
	if len(urls) == 0 {
		return nil, core.NewTaskValidationError(
			"required at least one url", nil, op,
		)
	}
	curls := make([]string, 0, len(urls))
	seen := make(map[string]bool, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			return nil, core.NewTaskValidationError(
				"got empty url", nil, op,
			)
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = true
		curls = append(curls, u)
	}
	return curls, nil
}

func createEvent(
	taskID string,
	eventType wal.EventType,
	when time.Time,
	payload any,
) (wal.Event, error) {
	if data, err := json.Marshal(payload); err != nil {
		return wal.Event{}, err
	} else {
		return wal.Event{
			CreatedAt: when,
			Version:   walVersion,
			TaskID:    taskID,
			Type:      eventType,

			Payload: data,
		}, nil
	}
}

func tryAsAppError(err error, op string) error {
	if err == nil {
		return nil
	}
	if appErr, ok := core.AsAppError(err); ok {
		return appErr.WithOper(op)
	}
	return internalError(op, "unexpected error", err)
}

func validationError(op, msg string) error {
	return core.NewAppErrorBuilder(core.ErrorCodeValidation).
		Message(msg).
		SafeToShow(true).
		Oper(op).
		Build()
}

func internalError(op, msg string, err error) error {
	return core.NewAppErrorBuilder(core.ErrorCodeInternal).
		Message(msg).
		Err(err).
		SafeToShow(false).
		Oper(op).
		Build()
}
