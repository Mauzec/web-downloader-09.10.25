package core

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

const testOp = "core.errors_test"

func TestAppErrorHTTPStatus(t *testing.T) {
	testCases := []struct {
		name string
		err  *AppError
		want int
	}{
		{name: "nil", err: nil, want: http.StatusInternalServerError},
		{
			name: "internal",
			err:  NewAppError(ErrorCodeInternal, "int", nil),
			want: http.StatusInternalServerError,
		},
		{
			name: "conflict",
			err:  NewTaskConflictError("confl", testOp),
			want: http.StatusConflict,
		},
		{
			name: "not found",
			err:  NewTaskNotFoundError("nf", testOp),
			want: http.StatusNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.HTTPStatus(); got != tc.want {
				t.Fatalf("HTTPStatus: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAppErrorPublicMessage(t *testing.T) {
	err := NewTaskInternalError(
		"internal salamander",
		errors.New("your bad"), testOp,
	)
	if got := err.PublicMessage(); got != "internal error" {
		t.Fatalf("PublicMessage: got %q, want internal error"+
			"because internal error not public", got)
	}

	safe := NewTaskConflictError("bad", testOp)
	if got := safe.PublicMessage(); got != "task bad already exists" {
		t.Fatalf("PublicMessage: got %q, want task bad already exists", got)
	}
}

func TestAppErrorCloneImmutability(t *testing.T) {
	root := NewTaskValidationError("bad input", nil, testOp)
	new := root.WithOper("core.errors_test")
	if new == root {
		t.Fatal("WithOper should copy the error")
	}
	if root.Operation != "" {
		t.Fatalf("root error mutated, but it shouldn't: %v", root)
	}
	if new.Operation != "core.errors_test" {
		t.Fatalf("new error operation wrong: %v", new)
	}

	new = root.WithMeta("key", "val1")
	if new.Meta["key"] != "val1" {
		t.Fatalf("got new.Meta[key] = %q, want val1", new.Meta["key"])
	}
	if root.Meta != nil {
		t.Fatalf("root.Meta should remain nil, got %v", root.Meta)
	}

	next := new.WithMeta("some", "val2")
	if len(new.Meta) != 1 {
		t.Fatalf("new.Meta size should remain 1, got %d", len(new.Meta))
	}
	if len(next.Meta) != 2 {
		t.Fatalf("next.Meta size should be 2, got %d", len(next.Meta))
	}
}

func TestAppErrorErrorsIsAndAs(t *testing.T) {
	root := NewTaskNotFoundError("nf", "core.errors_test")
	w := fmt.Errorf("wrap: %w", root)
	if !errors.Is(w, root) {
		t.Fatalf("errors.Is should match AppError codes")
	}
	e, ok := AsAppError(w)
	if !ok {
		t.Fatalf("AsAppError failed")
	}
	if e.Code != ErrorCodeNotFound {
		t.Fatalf("new code = %v, want %v", e.Code, ErrorCodeNotFound)
	}
}
