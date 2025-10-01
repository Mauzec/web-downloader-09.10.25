package api

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	requestIDContextKey = "request_id"
	taskIDContextKey    = "task_id"
)

// RequestIDMiddleware checks every request carries a request id.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		c.Set(requestIDContextKey, reqID)
		c.Writer.Header().Set("X-Request-ID", reqID)
		c.Next()
	}
}

func LoggingMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		path := c.FullPath()
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
		}
		if rid := GetRequestID(c); rid != "" {
			fields = append(fields, zap.String("request_id", rid))
		}
		if tid := GetTaskID(c); tid != "" {
			fields = append(fields, zap.String("task_id", tid))
		}
		if len(c.Errors) != 0 {
			fields = append(fields, zap.String("errors", c.Errors.String()))
		}

		logger.Info("request", fields...)
	}
}

func RecoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic caught",
					zap.Any("panic", r),
					zap.ByteString("stack", debug.Stack()),
					zap.String("reqid", GetRequestID(c)),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	}
}

func SetTaskID(c *gin.Context, taskID string) {
	c.Set(taskIDContextKey, taskID)
}

func GetTaskID(c *gin.Context) string {
	if v, ok := c.Get(taskIDContextKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func GetRequestID(c *gin.Context) string {
	v, ok := c.Get(requestIDContextKey)
	if ok {
		if s, ok := v.(string); ok {
			return s
		}
	}

	return ""
}
