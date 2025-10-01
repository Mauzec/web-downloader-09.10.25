package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var ErrNoTaskService = errors.New("task service is required")
var ErrNoDownloader = errors.New("downloader is required")

type Server struct {
	router *gin.Engine

	httpSrv *http.Server
}

type ServerOptions struct {
	TaskService taskService
	Downloader  downloader
	Logger      *zap.Logger
	Addr        string
}

func NewServer(opts *ServerOptions) (*Server, error) {
	if opts.TaskService == nil {
		return nil, ErrNoTaskService
	}
	if opts.Downloader == nil {
		return nil, ErrNoDownloader
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}

	router := gin.New()
	router.Use(
		RecoveryMiddleware(opts.Logger),
		RequestIDMiddleware(),
		LoggingMiddleware(opts.Logger),
	)

	h := NewHandler(opts.TaskService, opts.Downloader, opts.Logger)
	setupRouter(router, h)

	return &Server{
		router: router,
		httpSrv: &http.Server{
			Addr:    opts.Addr,
			Handler: router,
		}}, nil
}

func (s *Server) Run() error {
	return s.httpSrv.ListenAndServe()
}
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (server *Server) RunTLS(addr, certFile, keyFile string) error {
	return server.router.RunTLS(addr, certFile, keyFile)
}

func (s *Server) Router() http.Handler {
	return s.router
}

func setupRouter(router *gin.Engine, h *handler) {
	group := router.Group("/")
	group.POST("/tasks", h.createTask)
	group.GET("/tasks/:id", h.getTask)

	// group.GET("/healthz", func(c *gin.Context) {
	// 	c.JSON(http.StatusOK, gin.H{"status": "ok"})
	// })

	// group.GET("/tasks", h.listTasks)
}
