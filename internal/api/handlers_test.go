package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mauzec/web-downloader/internal/core"
	"github.com/stretchr/testify/require"
)

type mockDownloader struct {
	LastID      string
	EnqueueF    func(ctx context.Context, taskID string) error
	EnqueueAllF func(ctx context.Context) error
	CloseF      func()
}

func (mdl *mockDownloader) Enqueue(ctx context.Context, taskID string) error {
	mdl.LastID = taskID

	return mdl.EnqueueF(ctx, taskID)
}
func (mdl *mockDownloader) Close() {
	mdl.CloseF()

}
func (mdl *mockDownloader) EnqueueAll(ctx context.Context) error {
	return mdl.EnqueueAllF(ctx)
}

type mockTaskService struct {
	LastGetID       string
	LastCreatedURLs []string
	CreateTaskF     func(ctx context.Context, urls []string) (*core.Task, error)
	GetTaskF        func(ctx context.Context, id string) (*core.Task, error)
	SortedTasksF    func(ctx context.Context) ([]*core.Task, error)
}

func (mts *mockTaskService) CreateTask(ctx context.Context, urls []string) (*core.Task, error) {
	mts.LastCreatedURLs = slices.Clone(urls)
	return mts.CreateTaskF(ctx, urls)
}
func (mts *mockTaskService) GetTask(ctx context.Context, id string) (*core.Task, error) {
	mts.LastGetID = id
	return mts.GetTaskF(ctx, id)
}
func (mts *mockTaskService) SortedTasks(ctx context.Context) ([]*core.Task, error) {
	return mts.SortedTasksF(ctx)
}

var testTask = &core.Task{
	ID: "simon-task",
	Files: []*core.File{
		{URL: "http://docs.google.com/simon.data", Filename: "simon.data", Status: core.FileStatusCompleted, Size: 1234},
		{URL: "http://docs.google.com/simon.init", Filename: "simon.init", Status: core.FileStatusDownloading, Size: 1234},
	},
	Status: core.TaskStatusPartiallyCompleted,
	URLs: []string{
		"http://docs.google.com/simon.data",
		"http://docs.google.com/simon.init",
	},
}

func TestCreateTaskAPI(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	urls := []string{
		"http://google.com/1",
		"http://google.com/2",
	}
	createdTask := testTask.CloneTask()
	createdTask.ID = "simon1"
	wantResp := NewTaskResponse(createdTask.CloneTask())
	svc := &mockTaskService{
		CreateTaskF: func(ctx context.Context, reqURLs []string) (*core.Task, error) {
			require.Equal(t, urls, reqURLs)
			return createdTask.CloneTask(), nil
		},
	}
	dl := &mockDownloader{
		EnqueueF: func(ctx context.Context, taskID string) error {
			require.Equal(t, createdTask.ID, taskID)
			return nil
		},
	}
	h := NewHandler(svc, dl, nil)
	r := gin.New()
	r.POST("/tasks", h.createTask)

	body := `{"urls":["http://google.com/1","http://google.com/2"]}`
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	resp := TaskResponse{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, createdTask.ID, resp.ID)
	if wantResp != nil {
		require.Equal(t, wantResp.Status, resp.Status)
	}
	require.Equal(t, urls, svc.LastCreatedURLs)
	require.Equal(t, createdTask.ID, dl.LastID)
	require.Len(t, resp.Files, len(createdTask.Files))
	if len(resp.Files) > 0 && wantResp != nil && len(wantResp.Files) > 0 {
		require.Equal(t, createdTask.Files[0].URL, resp.Files[0].URL)
		require.Equal(t, wantResp.Files[0].Status, resp.Files[0].Status)
	}
}

// func TestGetTaskAPI(t *testing.T) {}
