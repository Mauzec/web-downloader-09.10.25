package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mauzec/web-downloader/internal/core"
	"go.uber.org/zap"
)

type downloader interface {
	Enqueue(ctx context.Context, taskID string) error
	EnqueueAll(ctx context.Context) error
	Close()
}
type taskService interface {
	CreateTask(ctx context.Context, urls []string) (*core.Task, error)
	GetTask(ctx context.Context, id string) (*core.Task, error)
	SortedTasks(ctx context.Context) ([]*core.Task, error)
}

type handler struct {
	tasks  taskService
	dl     downloader
	logger *zap.Logger
}

const handlerTimeout = 2 * time.Minute

func NewHandler(ts taskService, dl downloader, logger *zap.Logger) *handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &handler{tasks: ts, dl: dl, logger: logger}
}

func (h *handler) createTask(c *gin.Context) {
	req := CreateTaskRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.badRequestResponse(c, err)
		return
	}

	createCtx, cancelCreate := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancelCreate()

	t, err := h.tasks.CreateTask(createCtx, req.URLs)
	if err != nil {
		h.errorResponse(c, err)
		return
	}
	SetTaskID(c, t.ID)
	enqueueCtx, cancEnqueue := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancEnqueue()
	if err := h.dl.Enqueue(enqueueCtx, t.ID); err != nil {
		h.logger.Error("enqueue failed",
			zap.String("reqid", GetRequestID(c)),
			zap.String("taskid", t.ID),
			zap.Error(err),
		)
	}
	h.logger.Info("enqueued task",
		zap.String("reqid", GetRequestID(c)),
		zap.String("task_id", t.ID),
		zap.Int("files", len(t.Files)),
	)
	c.JSON(http.StatusCreated, NewTaskResponse(t))
}

func (h *handler) getTask(c *gin.Context) {
	id := c.Param("id")
	SetTaskID(c, id)
	ctx, canc := context.WithTimeout(c.Request.Context(), handlerTimeout)
	defer canc()

	t, err := h.tasks.GetTask(ctx, id)
	if err != nil {
		h.errorResponse(c, err)
		return
	}
	c.JSON(http.StatusOK, NewTaskResponse(t))
}

// func (h *handler) listTasks(c *gin.Context) {
// 	ctx, canc := context.WithTimeout(c.Request.Context(), handlerTimeout)
// 	defer canc()
// 	tasks, err := h.tasks.SortedTasks(ctx)
// 	if err != nil {
// 		h.errorResponse(c, err)
// 		return
// 	}
// 	c.JSON(http.StatusOK, NewTasksListResponse(tasks))
// }

func (h *handler) badRequestResponse(c *gin.Context, err error) {
	if c != nil && err != nil {
		c.Error(err) //nolint:errcheck
	}
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
		"error":   "bad request",
		"details": err.Error(),
	})
}

func (h *handler) errorResponse(c *gin.Context, err error) {
	if c != nil && err != nil {
		c.Error(err) //nolint:errcheck
	}
	if err == nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
		return
	}

	if appErr, ok := core.AsAppError(err); ok {
		s := appErr.HTTPStatus()
		p := gin.H{
			"error": appErr.PublicMessage(),
			"code":  appErr.Code,
		}
		if appErr.SafeToShow {
			switch {
			case appErr.Err != nil:
				p["details"] = appErr.Err.Error()
			case appErr.Message != "":
				p["details"] = appErr.Message
			}
		}
		h.logger.Warn("handler error",
			zap.String("reqid", GetRequestID(c)),
			zap.String("task_id", GetTaskID(c)),
			zap.String("error", err.Error()),
		)
		c.AbortWithStatusJSON(s, p)
		return
	}

	h.logger.Error("handler unknown error",
		zap.String("reqid", GetRequestID(c)),
		zap.String("task_id", GetTaskID(c)),
		zap.String("error", err.Error()),
	)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"error": "internal server error",
	})
}
