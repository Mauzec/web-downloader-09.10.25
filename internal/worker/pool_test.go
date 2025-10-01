package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubHandler struct {
	mu        sync.Mutex
	processed []Job
	delay     time.Duration
}

func (h *stubHandler) Handle(job Job) error {
	time.Sleep(h.delay)
	h.mu.Lock()
	h.processed = append(h.processed, job)
	h.mu.Unlock()
	return nil
}

func TestPoolProcessesJobs(t *testing.T) {
	t.Parallel()

	handler := &stubHandler{delay: 10 * time.Millisecond}
	pool, err := NewPool(2, handler, 4)
	require.NoError(t, err)

	err = pool.Start()
	require.NoError(t, err)

	ctx := context.Background()
	accepted := 0
	rejected := 0
	for i := range 5 {
		err = pool.Submit(ctx, Job{TaskID: "t", FileIndex: i, URL: "https://google.com"})
		if err != nil {
			require.ErrorIs(t, err, ErrPoolFull)
			rejected++
			continue
		}
		accepted++
	}

	pool.Stop()

	handler.mu.Lock()
	processed := len(handler.processed)
	handler.mu.Unlock()
	require.Equal(t, accepted, processed)
	require.LessOrEqual(t, rejected, 1)
}

func TestPoolSubmitAfterStop(t *testing.T) {
	t.Parallel()

	handler := &stubHandler{}
	pool, err := NewPool(1, handler, 1)
	require.NoError(t, err)
	require.NoError(t, pool.Start())

	pool.Stop()

	err = pool.Submit(context.Background(), Job{TaskID: "t", FileIndex: 0})
	require.ErrorIs(t, err, ErrPoolClosed)
}

func TestPoolSubmitContextCancelled(t *testing.T) {
	t.Parallel()

	handler := &stubHandler{}
	pool, err := NewPool(1, handler, 0)
	require.NoError(t, err)
	require.NoError(t, pool.Start())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = pool.Submit(ctx, Job{TaskID: "t", FileIndex: 0})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	pool.Stop()
}
