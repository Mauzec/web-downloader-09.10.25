package worker

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
)

type Job struct {
	TaskID    string
	URL       string
	FileIndex int
}

var ErrPoolClosed = errors.New("worker: pool closed")
var ErrPoolFull = errors.New("worker: queue full")

// ErrPollNotStarted says the pool should be started before work
var ErrPoolNotStarted = errors.New("worker: pool not started")

// Pool runs download jobs with BOUNDED! concurrency.
type Pool struct {
	handler Handler
	workers int
	jobs    chan Job

	started atomic.Bool
	closed  atomic.Bool

	stopOnce sync.Once
	wg       sync.WaitGroup
	done     chan struct{}
}

func NewPool(workers int, handler Handler, queueSize int) (*Pool, error) {
	if workers <= 0 {
		return nil, errors.New("worker: workers number cant be <= 0")
	}
	if handler == nil {
		return nil, errors.New("worker:required handler")
	}

	if queueSize <= 0 {
		queueSize = workers * 4
	}

	return &Pool{
		handler: handler,
		workers: workers,
		jobs:    make(chan Job, queueSize),

		done: make(chan struct{}),
	}, nil
}

func (p *Pool) Start() error {
	if !p.started.CompareAndSwap(false, true) {
		// its started
		return nil
	}
	for range p.workers {
		p.wg.Add(1)
		go p.run()
	}
	return nil
}
func (p *Pool) Stop() {
	p.stopOnce.Do(func() {
		p.closed.Store(true)
		close(p.done)
		close(p.jobs)
		p.wg.Wait()
	})
}

func (p *Pool) Submit(ctx context.Context, job Job) (err error) {
	if !p.started.Load() {
		return ErrPoolNotStarted
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if p.closed.Load() {
		return ErrPoolClosed
	}
	defer func() {
		r := recover()
		if r != nil {
			switch v := r.(type) {
			case string:
				if strings.Contains(v, "closed chan") {
					err = ErrPoolClosed
					return
				}
			case error:
				if strings.Contains(v.Error(), "closed chan") {
					err = ErrPoolClosed
					return
				}
			}
			panic(r)
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrPoolClosed
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrPoolClosed
	case p.jobs <- job:
		return nil
	default:
		return ErrPoolFull
	}
}

func (p *Pool) run() {
	defer p.wg.Done()
	for job := range p.jobs {
		if err := p.handler.Handle(job); err != nil {
			log.Printf("worker: handle job %+v: %v", job, err)
		}
	}
}
