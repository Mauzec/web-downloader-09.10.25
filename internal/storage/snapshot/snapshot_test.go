package snapshot_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/mauzec/web-downloader/internal/storage/snapshot"
	"github.com/stretchr/testify/require"
)

func TestReadMissSnapshot(t *testing.T) {
	t.Parallel()
	var (
		ctx  = context.Background()
		path = filepath.Join(t.TempDir(), "snapshot.json")
	)
	// if file doesnt exist, should return nil,nil
	ss, err := snapshot.Read(ctx, path)
	require.NoErrorf(t, err, "snapshot read: %v", err)
	require.Nilf(t, ss, "expected nil snapshot, got %#v", ss)
}

func TestWriteReadSnapshot(t *testing.T) {
	t.Parallel()
	var (
		ctx  = context.Background()
		path = filepath.Join(t.TempDir(), "snapshot.json")
	)
	now := time.Now().UTC()
	task := core.NewTask("simon", &now, "https://docs.google.com/ss/simon")

	ss := &snapshot.Snapshot{
		CreatedAt: now,
		Version:   snapshot.CurrentVersion,
		Tasks:     []*core.Task{task},
	}

	err := snapshot.Write(ctx, path, ss)
	require.NoErrorf(t, err, "snapshot write: %v", err)

	got, err := snapshot.Read(ctx, path)
	require.NoErrorf(t, err, "snapshot read: %v", err)
	require.NotNilf(t, got, "got nil snapshot")
	require.Equalf(t, ss.Version, got.Version,
		"version not equal: got %d, want %d", got.Version, ss.Version,
	)
	require.Truef(t, len(got.Tasks) == 1 && got.Tasks[0].ID == task.ID,
		"tasks not equal: got %#v", got.Tasks,
	)
}
