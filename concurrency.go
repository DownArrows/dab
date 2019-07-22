package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

func IsCancellation(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func SleepCtx(ctx context.Context, duration time.Duration) bool {
	select {
	case <-time.After(duration):
		return true
	case <-ctx.Done():
		return false
	}
}

type Task func(context.Context) error

type RetryOptions struct {
	Times       int      `json:"times"`
	MaxInterval Duration `json:"max_interval"`
}

func Retry(opts RetryOptions, task Task) Task {
	backoff := time.Second
	retries := 0
	return func(ctx context.Context) error {
		for {
			if err := task(ctx); err != nil {
				if !IsCancellation(err) && (retries < opts.Times || opts.Times == -1) {
					if opts.MaxInterval.Value-backoff > 0*time.Second {
						SleepCtx(ctx, backoff)
						backoff *= 2
					} else {
						SleepCtx(ctx, opts.MaxInterval.Value)
					}
					retries += 1
				} else {
					return err
				}
			} else {
				break
			}
		}
		return nil
	}
}

// Launches and shuts down a group of goroutine which take a context and return an error.
// Use TaskGroup.Spawn to launch functions asynchronously,
// and once you're done use TaskGroup.Wait to wait on them.
// To cancel a TaskGroup, use TaskGroup.Cancel, or cancel the context you passed
// when creating the TaskGroup.
// TaskGroup.Wait returns a type of error that contains multiple errors and which
// can be converted to a normal error that can be usefully compared to nil.
type TaskGroup struct {
	parent  *TaskGroup
	Cancel  context.CancelFunc
	Context context.Context
	Errors  *ErrorGroup
	wait    *sync.WaitGroup
}

func NewTaskGroup(parent_ctx context.Context) *TaskGroup {
	ctx, cancel := context.WithCancel(parent_ctx)
	return &TaskGroup{
		Cancel:  cancel,
		Context: ctx,
		Errors:  NewErrorGroup(),
		wait:    &sync.WaitGroup{},
	}
}

func (tg *TaskGroup) SpawnCtx(cb Task) {
	tg.wait.Add(1)
	go func() {
		if err := cb(tg.Context); err != nil && !IsCancellation(err) {
			tg.Errors.Add(err)
			if tg.parent != nil {
				tg.parent.Errors.Add(err)
			}
			tg.Cancel()
		}
		tg.wait.Done()
	}()
}

func (tg *TaskGroup) Spawn(cb func()) {
	tg.wait.Add(1)
	go func() {
		cb()
		tg.wait.Done()
	}()
}

func (tg *TaskGroup) SubGroup() *TaskGroup {
	sub_tg := NewTaskGroup(tg.Context)
	sub_tg.parent = tg
	return sub_tg
}

func (tg *TaskGroup) Wait() *ErrorGroup {
	defer tg.Cancel()
	tg.wait.Wait()
	return tg.Errors
}

type ErrorGroup struct {
	mutex  *sync.RWMutex
	errors []error
}

func NewErrorGroup() *ErrorGroup {
	return &ErrorGroup{
		mutex:  &sync.RWMutex{},
		errors: []error{},
	}
}

func (eg *ErrorGroup) Add(err error) {
	eg.mutex.Lock()
	defer eg.mutex.Unlock()
	eg.errors = append(eg.errors, err)
}

func (eg *ErrorGroup) Errors() []error {
	eg.mutex.RLock()
	defer eg.mutex.RUnlock()
	errors := make([]error, len(eg.errors))
	copy(errors, eg.errors)
	return errors
}

func (eg *ErrorGroup) Len() int {
	eg.mutex.RLock()
	defer eg.mutex.RUnlock()
	return len(eg.errors)
}

func (eg *ErrorGroup) Error() string {
	errors := eg.Errors()

	if len(errors) == 0 {
		return ""
	} else if len(errors) == 1 {
		return errors[0].Error()
	}

	msgs := make([]string, 1, len(errors))

	msgs[0] = fmt.Sprintf("%d error(s):", len(errors))
	for i, err := range errors {
		msgs = append(msgs, fmt.Sprintf("\t%d. %v", i+1, err))
	}

	return strings.Join(msgs, "\n")
}

func (eg *ErrorGroup) ToError() error {
	if eg.Len() == 0 {
		return nil
	}
	return eg
}
