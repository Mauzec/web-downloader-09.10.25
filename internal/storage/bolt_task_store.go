package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage/wal"
	bolt "go.etcd.io/bbolt"
)

type BoltTaskStore struct {
	db *bolt.DB
}

const boltTasksBucket = "webdl-tasks"

func NewBoltTaskStore(path string) (*BoltTaskStore, error) {
	if path == "" {
		return nil, errors.New("storage:required bolt path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("storage: create bolt dir: %w", err)
	}
	db, err := bolt.Open(path, 0o600,
		&bolt.Options{Timeout: time.Second},
	)
	if err != nil {
		return nil, fmt.Errorf("storage: opening bolt: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, berr := tx.CreateBucketIfNotExists([]byte(boltTasksBucket))
		return berr
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: cant init bucket: %w", err)
	}

	return &BoltTaskStore{db: db}, nil
}

func (s *BoltTaskStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// CreateTask stores a new task.
func (s *BoltTaskStore) CreateTask(ctx context.Context, task *core.Task, _ wal.Event) error {
	if s.db == nil {
		return errors.New("storage: bolt not init")
	} else if task == nil {
		return errors.New("storage: required task")
	} else if err := ctx.Err(); err != nil {
		return err
	}

	p, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("storage: cant marshal task: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(boltTasksBucket))
		if b == nil {
			return errors.New("storage: bucket miss")
		}
		if b.Get([]byte(task.ID)) != nil {
			return fmt.Errorf("storage: task %s already here", task.ID)
		}
		return b.Put([]byte(task.ID), p)
	})
}

func (s *BoltTaskStore) UpdateTask(ctx context.Context, task *core.Task, _ ...wal.Event) error {
	if s.db == nil {
		return errors.New("storage: bolt not init")
	} else if task == nil {
		return errors.New("storage: required task")
	} else if err := ctx.Err(); err != nil {
		return err
	}

	p, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("storage: cant marshal task: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(boltTasksBucket))
		if bucket == nil {
			return errors.New("storage: bucket miss")
		}
		if bucket.Get([]byte(task.ID)) == nil {
			return fmt.Errorf("storage: task %s not found", task.ID)
		}
		return bucket.Put([]byte(task.ID), p)
	})
}

func (s *BoltTaskStore) LoadAll(ctx context.Context) ([]*core.Task, error) {
	ts, err := s.listTasks(ctx)
	if err != nil {
		return nil, err
	}
	res := make([]*core.Task, 0, len(ts))
	for _, t := range ts {
		res = append(res, t.CloneTask())
	}
	return res, nil
}

func (s *BoltTaskStore) ListTasks(ctx context.Context) ([]*core.Task, error) {
	return s.LoadAll(ctx)
}

func (s *BoltTaskStore) listTasks(ctx context.Context) ([]*core.Task, error) {
	if s.db == nil {
		return nil, errors.New("storage: bolt not init")
	} else if err := ctx.Err(); err != nil {
		return nil, err
	}

	ts := make([]*core.Task, 0)
	if err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(boltTasksBucket))
		if bucket == nil {
			return errors.New("storage: bucket miss")
		}
		return bucket.ForEach(func(_, v []byte) error {
			t := &core.Task{}
			if err := json.Unmarshal(v, t); err != nil {
				return fmt.Errorf("storage: cant unmarshal task: %w", err)
			}
			ts = append(ts, t)
			return nil
		})
	}); err != nil {
		return nil, err
	}
	core.SortTasks(ts)
	return ts, nil
}

func (s *BoltTaskStore) FlushSnapshot(ctx context.Context) error {
	if s.db == nil {
		return errors.New("storage: bolt not init")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Sync()
}

func (s *BoltTaskStore) GetTaskByID(ctx context.Context, id string) (*core.Task, error) {
	const op = "storage.BoltTaskStore.GetTaskByID"
	if s.db == nil {
		return nil, errors.New("storage: why u call me if im nil?")
	} else if err := ctx.Err(); err != nil {
		return nil, err
	}

	task := &core.Task{}
	if err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(boltTasksBucket))
		if bucket == nil {
			return errors.New("storage: bucket miss")
		}
		value := bucket.Get([]byte(id))
		if value == nil {
			return nil
		}
		res := &core.Task{}
		if err := json.Unmarshal(value, res); err != nil {
			return fmt.Errorf("storage: cant unmarshal task: %w", err)
		}
		task = res
		return nil
	}); err != nil {
		return nil, err
	}
	if task == nil {
		return nil, core.NewTaskNotFoundError(id, op)
	}
	return task.CloneTask(), nil
}
