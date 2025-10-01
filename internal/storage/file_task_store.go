package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage/snapshot"
	"github.com/mauzec/web-downloader/internal/storage/wal"
	"github.com/mauzec/web-downloader/internal/utils"
)

type FileTaskStore struct {
	tasks map[string]*core.Task

	snapshotPath string
	walPath      string

	log wal.AppendOnlyLog
	mu  sync.RWMutex
}

func NewFileTaskStore(snapshotPath, walPath string) (*FileTaskStore, error) {
	log, err := wal.NewFileLog(walPath)
	if err != nil {
		return nil, err
	}
	return &FileTaskStore{
		snapshotPath: snapshotPath,
		walPath:      walPath,
		tasks:        make(map[string]*core.Task),
		log:          log,
	}, nil
}

// Close closes wal log.
func (st *FileTaskStore) Close() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.log == nil {
		return nil
	}
	err := st.log.Close()
	st.log = nil
	return err
}

func (st *FileTaskStore) CreateTask(ctx context.Context, task *core.Task, ev wal.Event) error {
	if task == nil {
		return errors.New("store: required task")
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if _, ok := st.tasks[task.ID]; ok {
		return errors.New("store: task " + task.ID + " already exists")
	}

	if err := st.flushAppend(ctx, ev); err != nil {
		return err
	}

	st.tasks[task.ID] = task.CloneTask()

	return nil
}

func (st *FileTaskStore) UpdateTask(ctx context.Context, task *core.Task, evs ...wal.Event) error {
	if task == nil {
		return errors.New("store: required task")
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if _, ok := st.tasks[task.ID]; !ok {
		return errors.New("store: task " + task.ID + " not found")
	}

	if len(evs) > 0 {
		if err := st.flushAppend(ctx, evs...); err != nil {
			return err
		}
	}

	st.tasks[task.ID] = task.CloneTask()
	return nil
}

func (st *FileTaskStore) LoadAll(ctx context.Context) ([]*core.Task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	tasks := make(map[string]*core.Task)
	var ss *snapshot.Snapshot
	var err error

	if ss, err = snapshot.Read(ctx, st.snapshotPath); err != nil {
		return nil, err
	}
	if ss != nil {
		for _, t := range ss.Tasks {

			tasks[t.ID] = t.CloneTask()
		}
	}

	evs, err := wal.ReadAll(ctx, st.walPath)
	if err != nil {
		return nil, err
	}

	if err := applyEvents(tasks, evs); err != nil {
		return nil, err
	}

	st.tasks = tasks

	res := make([]*core.Task, 0, len(tasks))

	for _, t := range tasks {
		res = append(res, t.CloneTask())
	}

	core.SortTasks(res)

	return res, nil
}

func (st *FileTaskStore) FlushSnapshot(ctx context.Context) error {

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.log == nil {
		return errors.New("store: wal log not initialized")
	}

	copy := make([]*core.Task, 0, len(st.tasks))
	for _, t := range st.tasks {
		copy = append(copy, t.CloneTask())
	}
	core.SortTasks(copy)

	ss := &snapshot.Snapshot{
		CreatedAt: time.Now().UTC(),
		Version:   snapshot.CurrentVersion,
		Tasks:     copy,
	}

	if err := st.log.Flush(ctx); err != nil {
		return err
	}

	if err := snapshot.Write(ctx, st.snapshotPath, ss); err != nil {
		return err
	}

	return st.backupAndResetLog()
}

func (st *FileTaskStore) flushAppend(ctx context.Context, evs ...wal.Event) error {
	if st.log == nil {
		return errors.New("store: wallog not initialize")
	}
	if err := st.log.Append(ctx, evs...); err != nil {
		return err
	}
	return st.log.Flush(ctx)
}

func (st *FileTaskStore) backupAndResetLog() error {
	if st.log == nil {
		return errors.New("store: stop using not initialized wal log")
	}
	if err := st.log.Close(); err != nil {
		return fmt.Errorf("store: why u use closed wal log: %w", err)
	}

	st.log = nil

	if err := os.MkdirAll(
		filepath.Join(filepath.Dir(st.walPath), "old"),
		0o755,
	); err != nil {
		return fmt.Errorf("store: cant create old wal dir: %w", err)
	}

	oldPath := filepath.Join(
		filepath.Dir(st.walPath),
		"old",
		fmt.Sprintf(
			"wal-%s.log",
			time.Now().UTC().Format("20060102T150405Z"),
		),
	)
	if err := os.Rename(st.walPath, oldPath); err != nil {
		return fmt.Errorf("store: cant rename wal to old: %w", err)
	}

	if nl, err := wal.NewFileLog(st.walPath); err != nil {
		return fmt.Errorf("store: cant create new wal log: %w", err)
	} else {
		st.log = nl
		return nil
	}
}

func applyEvents(tasks map[string]*core.Task, evs []wal.Event) error {
	for _, ev := range evs {
		switch ev.Type {
		case wal.EventTaskCreated:
			p := wal.TaskCreatedPayload{}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return fmt.Errorf("store: decoding task_created: %w", err)
			}
			if p.Task == nil {
				continue
			}
			tasks[p.Task.ID] = p.Task.CloneTask()
		case wal.EventTaskStatus:
			p := wal.TaskStatusPayload{}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return fmt.Errorf("store: decoding task_status: %w", err)
			}
			if t := tasks[ev.TaskID]; t != nil {
				t.Status = p.Status
				t.UpdatedAt = utils.TimePtr(ev.CreatedAt)
			}
		case wal.EventFileStatus:
			p := wal.FileStatusPayload{}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return fmt.Errorf("storage: decoding file_status: %w", err)
			}

			if t := tasks[ev.TaskID]; t != nil {
				if p.FileIndex >= 0 && p.FileIndex < len(t.Files) {
					f := t.Files[p.FileIndex]
					f.Status = p.Status
					f.Error = p.Error

					if p.Size > 0 {
						f.Size = p.Size
					}
					if t.UpdatedAt == nil || t.UpdatedAt.Before(ev.CreatedAt) {
						t.UpdatedAt = utils.TimePtr(ev.CreatedAt)
					}
					now := ev.CreatedAt
					t.UpdateStatus(&now)
				}
			}
		case wal.EventFileMeta:
			p := wal.FileMetaPayload{}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return fmt.Errorf("storage: decoding file_meta: %w", err)
			}
			if t := tasks[ev.TaskID]; t != nil {
				if p.FileIndex < 0 || p.FileIndex >= len(t.Files) {
					continue
				}

				f := t.Files[p.FileIndex]
				if p.FileName != "" {
					f.Filename = p.FileName
				}
				f.StartedAt = p.StartedAt
				f.CompletedAt = p.CompletedAt

			}
		default:
			return fmt.Errorf("storage: get uncaught event %v", ev.Type)
		}
	}
	return nil
}
