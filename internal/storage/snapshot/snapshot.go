package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
)

const CurrentVersion = 1

type Snapshot struct {
	Version int `json:"version"`

	Tasks []*core.Task `json:"tasks"`

	CreatedAt time.Time `json:"created_at"`
}

// Write persists the snapshot to the path.
func Write(ctx context.Context, path string, ss *Snapshot) error {
	if ss == nil {
		return errors.New("snapshot: got nil snapshot")
	} else if path == "" {
		return errors.New("snapshot: required path")
	} else if err := ctx.Err(); err != nil {
		return err
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("snapshot: create dir: %w", err)
	}
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(
		tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o644,
	)
	if err != nil {
		return fmt.Errorf("snapshot: open tmp: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ss); err != nil {
		closeErr := f.Close()
		if closeErr != nil {
			return fmt.Errorf("snapshot: encode: %v: close:%w", err, closeErr)
		}
		return fmt.Errorf("snapshot: encode: %w", err)
	} else if err := f.Sync(); err != nil {
		closeErr := f.Close()
		if closeErr != nil {
			return fmt.Errorf("snapshot: fsync: %v: close:%w", err, closeErr)
		}
		return fmt.Errorf("snapshot: fsync: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("snapshot: close: %w", err)
	} else if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("snapshot: rename tmp: %w", err)
	} else {
		return nil
	}
}

// Read loads a snapshot from disk.
func Read(ctx context.Context, path string) (*Snapshot, error) {
	if path == "" {
		return nil, errors.New("snapshot: required path")
	} else if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := os.Open(path)

	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("snapshot: open: %w", err)
	}
	defer f.Close()

	ss := Snapshot{}
	dec := json.NewDecoder(f)
	if err := dec.Decode(&ss); err != nil {
		return nil, fmt.Errorf("snapshot: decode: %w", err)
	}
	return &ss, nil
}
