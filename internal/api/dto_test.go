package api

import (
	"testing"

	"github.com/mauzec/web-downloader/internal/core"
	"github.com/stretchr/testify/require"
)

func TestNewTaskResponseQueuedStatus(t *testing.T) {
	task := &core.Task{
		ID:     "queued",
		Status: core.TaskStatusInProgress,
		Files: []*core.File{
			{
				URL:    "https://docs.google.com.com/1",
				Status: core.FileStatusQueued,
			},
		},
	}

	resp := NewTaskResponse(task)
	require.NotNil(t, resp)
	require.Equal(t, "queued", resp.Files[0].Status)
}
