package wal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// AppendOnlyLog define the min operations for WAL.
// Implementations should guarantee ordering and durability
// of apended events and be concurrent-safe
type AppendOnlyLog interface {
	Append(ctx context.Context, events ...Event) error
	Flush(ctx context.Context) error
	Close() error
}

type FileLog struct {
	Closed bool

	file *os.File
	wrt  *bufio.Writer

	path string
	mu   sync.Mutex
}

const DefaultBufSize = 64 * 1024
const MaxScanBufSize = 6 * 1024 * 1024

func NewFileLog(path string) (*FileLog, error) {
	if path == "" {
		return nil, errors.New("wal: path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("wal: create dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal: open file: %w", err)
	}

	return &FileLog{
		wrt:  bufio.NewWriterSize(f, DefaultBufSize),
		file: f,
		path: path,
	}, nil
}

func (fl *FileLog) ResetBufferSize(newSize int) {
	if fl.Closed {
		return
	}
	if newSize <= 0 {
		newSize = DefaultBufSize
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	fl.wrt = bufio.NewWriterSize(fl.file, newSize)
}

func (fl *FileLog) Append(ctx context.Context, evs ...Event) error {
	if len(evs) == 0 || fl.Closed {
		return nil
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	for _, ev := range evs {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("wal: encode event: %w", err)
		}
		if _, err := fl.wrt.Write(data); err != nil {
			return fmt.Errorf("wal: write ev: %w", err)
		}
		if err := fl.wrt.WriteByte('\n'); err != nil {
			return fmt.Errorf("wal: write ev: %w", err)
		}
	}
	return nil
}

func (fl *FileLog) Flush(ctx context.Context) error {
	if fl.Closed {
		return nil
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fl.wrt.Flush(); err != nil {
		return fmt.Errorf("wal: flush: %w", err)
	}
	if err := fl.file.Sync(); err != nil {
		return fmt.Errorf("wal: fsync: %w", err)
	}
	return nil
}

func (fl *FileLog) Close() error {
	if fl.file == nil || fl.wrt == nil {
		return nil
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	combErr := errors.New("wal: close errors")
	gotErr := false

	if err := fl.wrt.Flush(); err != nil && !errors.Is(err, os.ErrClosed) {
		combErr = fmt.Errorf("%w: flush: %v", combErr, err)
		gotErr = true
	}
	if err := fl.file.Sync(); err != nil {
		combErr = fmt.Errorf("%w: fsync: %v", combErr, err)
		gotErr = true
	}
	if err := fl.file.Close(); err != nil {
		combErr = fmt.Errorf("%w: close: %v", combErr, err)
		gotErr = true
	}
	fl.wrt = nil
	fl.file = nil
	fl.Closed = true
	if !gotErr {
		return nil
	}
	return combErr
}

func (fl *FileLog) Path() string {
	return fl.path
}

func ReadAll(ctx context.Context, path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("wal: readll open: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, DefaultBufSize)
	sc.Buffer(buf, MaxScanBufSize)
	evs := make([]Event, 0, 256)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bytes := sc.Bytes()
		if len(bytes) == 0 {
			continue
		}

		ev := Event{}
		if err := json.Unmarshal(bytes, &ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			var se *json.SyntaxError
			if errors.As(err, &se) {
				break
			}
			return nil, fmt.Errorf("wal: decode event: %w", err)
		}
		evs = append(evs, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("wal: scan: %w", err)
	}
	return evs, nil
}
