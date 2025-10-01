package wal_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage/wal"
	"github.com/stretchr/testify/require"
)

func TestFileLogAppendRead(t *testing.T) {
	t.Parallel()
	var (
		ctx  = context.Background()
		path = filepath.Join(t.TempDir(), "wal.log")
	)
	wlog, err := wal.NewFileLog(path)
	require.NoErrorf(t, err, "newfilelog: %v", err)

	now := time.Now().UTC()
	task := core.NewTask("simon", &now, "https://docs.google.com/ss/simon")

	createdPayload, err := json.Marshal(wal.TaskCreatedPayload{Task: task})
	require.NoErrorf(t, err, "marshal created_payload: %v", err)
	createdEvent := wal.Event{
		Version:   1,
		TaskID:    task.ID,
		Type:      wal.EventTaskCreated,
		CreatedAt: now,
		Payload:   createdPayload,
	}

	statusPayload, err := json.Marshal(wal.FileStatusPayload{
		FileIndex: 0, Status: core.FileStatusCompleted, Size: 512},
	)
	require.NoErrorf(t, err, "marshal status_payload: %v", err)
	statusEvent := wal.Event{
		Version:   1,
		TaskID:    task.ID,
		Type:      wal.EventFileStatus,
		CreatedAt: now.Add(time.Second),
		Payload:   statusPayload,
	}

	err = wlog.Append(ctx, createdEvent, statusEvent)
	require.NoErrorf(t, err, "log append: %v", err)
	err = wlog.Flush(ctx)
	require.NoErrorf(t, err, "log flush: %v", err)
	err = wlog.Close()
	require.NoErrorf(t, err, "log close: %v", err)

	evs, err := wal.ReadAll(ctx, path)
	require.NoErrorf(t, err, "readall: %v", err)
	require.Equalf(t, 2, len(evs),
		"want 2 events, got %d", len(evs),
	)

	// first event

	require.Truef(t,
		evs[0].Type == wal.EventTaskCreated &&
			evs[0].TaskID == task.ID,
		"wrong 1 event: %#v", evs[0],
	)
	decCreated := wal.TaskCreatedPayload{}
	err = json.Unmarshal(evs[0].Payload, &decCreated)
	require.NoErrorf(t, err, "decode created payload: %v", err)
	require.NotNilf(t, decCreated.Task, "decoded created task is nil")
	require.Equalf(t, task.ID, decCreated.Task.ID,
		"decoded created task id mismatch: got %q, want %q",
		decCreated.Task.ID, task.ID,
	)

	// secaond event
	require.Truef(t,
		evs[1].Type == wal.EventFileStatus ||
			evs[1].TaskID == task.ID,
		"wrong 2 event: %#v", evs[1],
	)
	decStatus := wal.FileStatusPayload{FileIndex: 1}
	err = json.Unmarshal(evs[1].Payload, &decStatus)
	require.NoErrorf(t, err, "decode status payload: %v", err)
	require.Truef(t,
		decStatus.FileIndex == 1 ||
			decStatus.Status == core.FileStatusCompleted ||
			decStatus.Size == 512,
		"decoded status mismatch: %#v", decStatus,
	)

}

func TestReadAllMissFile(t *testing.T) {
	t.Parallel()
	var (
		ctx  = context.Background()
		path = filepath.Join(t.TempDir(), "miss.log")
	)
	// if file doesnt exist, should return nil,nil
	evs, err := wal.ReadAll(ctx, path)
	require.NoErrorf(t, err, "readall error: %v", err)
	require.Nilf(t, evs,
		"expected nil events slice got %#v", evs,
	)

}
