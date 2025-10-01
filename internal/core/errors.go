package core

import (
	"errors"
	"maps"
	"net/http"
)

type ErrorCode int

const (
	ErrorCodeInternal ErrorCode = iota
	ErrorCodeValidation
	ErrorCodeConflict
	ErrorCodeNotFound
)

type AppError struct {
	Code    ErrorCode
	Message string
	Err     error

	Operation   string
	Meta        map[string]string
	RetryPolicy bool
	// SafeToShow indicates is safe to show msg to users.
	SafeToShow bool
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

func (e *AppError) Is(target error) bool {
	if t, ok := target.(*AppError); !ok {
		return false
	} else {
		return e.Code == t.Code
	}
}

func NewAppError(code ErrorCode, message string, err error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

func (e *AppError) HTTPStatus() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	switch e.Code {
	case ErrorCodeValidation:
		return http.StatusBadRequest
	case ErrorCodeConflict:
		return http.StatusConflict
	case ErrorCodeNotFound:
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (e *AppError) PublicMessage() string {
	if e == nil {
		return "internal error"
	}
	if e.SafeToShow {
		return e.Message
	}
	return "internal error"
}

// Clone performs a copy of the error + deep-copy of Meta.
func (e *AppError) Clone() *AppError {
	if e == nil {
		return nil
	}
	c := *e
	if e.Meta == nil {
		return &c
	}

	c.Meta = make(map[string]string, len(e.Meta))
	maps.Copy(c.Meta, e.Meta)

	return &c
}

// WithOper returns a new copy of error with operation
// Use AppErrorBuilder if you just create new error.
func (e *AppError) WithOper(o string) *AppError {
	if e == nil {
		return nil
	}
	c := e.Clone()
	c.Operation = o

	return c
}

// WithOper returns a new copy of error with new key-value meta added
// Use AppErrorBuilder if you just create new error.
func (e *AppError) WithMeta(k, v string) *AppError {
	if e == nil {
		return nil
	}
	c := e.Clone()
	if c.Meta == nil {
		c.Meta = make(map[string]string, 1)
	}
	c.Meta[k] = v
	return c
}

func (e *AppError) WithMetas(k []string, v []string) *AppError {
	if e == nil {
		return nil
	}
	if len(k) != len(v) {
		return e
	}
	c := e.Clone()
	if c.Meta == nil {
		c.Meta = make(map[string]string, len(k))
	}
	for i := range k {
		c.Meta[k[i]] = v[i]
	}
	return c
}

// WithRetryPolicy returns a new copy of error with RetryPolicy set.
// Use AppErrorBuilder if you just create new error.
func (e *AppError) WithRetryPolicy(r bool) *AppError {
	if e == nil {
		return nil
	}
	c := e.Clone()
	c.RetryPolicy = r
	return c
}

// WithSafeToShow returns a new copy of error with SafeToShow set.
// Use AppErrorBuilder if you just create new error.
func (e *AppError) WithSafeToShow(safe bool) *AppError {
	if e == nil {
		return nil
	}
	c := e.Clone()
	c.SafeToShow = safe
	return c
}

func AsAppError(err error) (*AppError, bool) {
	if err == nil {
		return nil, false
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

type AppErrorBuilder struct {
	code    ErrorCode
	message string
	err     error

	operation   string
	meta        map[string]string
	retryPolicy bool
	safeToShow  bool
}

func NewAppErrorBuilder(code ErrorCode) *AppErrorBuilder {
	return &AppErrorBuilder{
		code: code,
	}
}
func (b *AppErrorBuilder) Message(m string) *AppErrorBuilder {
	b.message = m
	return b
}
func (b *AppErrorBuilder) Err(e error) *AppErrorBuilder {
	b.err = e
	return b
}
func (b *AppErrorBuilder) Oper(o string) *AppErrorBuilder {
	b.operation = o
	return b
}
func (b *AppErrorBuilder) Meta(k, v string) *AppErrorBuilder {
	if b.meta == nil {
		b.meta = make(map[string]string, 1)
	}
	b.meta[k] = v
	return b
}
func (b *AppErrorBuilder) Metas(k []string, v []string) *AppErrorBuilder {
	if len(k) != len(v) {
		return b
	}
	if b.meta == nil {
		b.meta = make(map[string]string, len(k))
	}
	for i := range k {
		b.meta[k[i]] = v[i]
	}
	return b
}
func (b *AppErrorBuilder) RetryPolicy(r bool) *AppErrorBuilder {
	b.retryPolicy = r
	return b
}
func (b *AppErrorBuilder) SafeToShow(safe bool) *AppErrorBuilder {
	b.safeToShow = safe
	return b
}
func (b *AppErrorBuilder) Build() *AppError {
	meta := b.meta
	b.meta = nil // if builder is reused
	return &AppError{
		Code:        b.code,
		Message:     b.message,
		Err:         b.err,
		Operation:   b.operation,
		Meta:        meta,
		RetryPolicy: b.retryPolicy,
		SafeToShow:  b.safeToShow,
	}
}

// Some useful constructors.

func NewTaskInternalError(message string, err error, op string) *AppError {
	return NewAppErrorBuilder(ErrorCodeInternal).
		Message(message).
		Err(err).
		SafeToShow(false).
		Build()
}

func NewTaskValidationError(message string, err error, op string) *AppError {
	return NewAppErrorBuilder(ErrorCodeValidation).
		Message(message).
		Err(err).
		SafeToShow(true).
		Build()
}

func NewTaskConflictError(taskID string, op string) *AppError {
	return NewAppErrorBuilder(ErrorCodeConflict).
		Message("task " + taskID + " already exists").
		SafeToShow(true).
		Build()
}

func NewTaskNotFoundError(taskID string, op string) *AppError {
	return NewAppErrorBuilder(ErrorCodeNotFound).
		Message("task " + taskID + " not found").
		SafeToShow(true).
		Build()
}
