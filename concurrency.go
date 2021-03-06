package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// IsCancellation returns whether the error corresponds to a cancellation.
func IsCancellation(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

// SleepCtx sleeps with the ability to be cancelled,
// and returns whether it has slept without being cancelled (makes use in for loops easier).
func SleepCtx(ctx context.Context, duration time.Duration) bool {
	select {
	case <-time.After(duration):
		return true
	case <-ctx.Done():
		return false
	}
}

// Task is a function that can be managed by a TaskGroup.
type Task func(context.Context) error

// TaskGroup launches and shuts down a group of goroutine which take a context and return an error.
// Use TaskGroup.Spawn to launch functions asynchronously,
// and once you're done use TaskGroup.Wait to wait on them.
//
// To cancel a TaskGroup, use TaskGroup.Cancel, or cancel the context you passed
// when creating the TaskGroup.
// TaskGroup.Wait returns a type of error that contains multiple errors and which
// can be converted to a normal error that can be usefully compared to nil.
//
// To create a sub-group, simply create a new TaskGroup with the parent group's context,
// and have the parent group wait on the sub-group with SpawnCtx, so that errors can propagate.
type TaskGroup struct {
	Cancel  context.CancelFunc
	Context context.Context
	Errors  *ErrorGroup
	wait    *sync.WaitGroup
}

// NewTaskGroup returns a new TaskGroup which depends on a parent context.
func NewTaskGroup(parentCtx context.Context) *TaskGroup {
	ctx, cancel := context.WithCancel(parentCtx)
	return &TaskGroup{
		Cancel:  cancel,
		Context: ctx,
		Errors:  NewErrorGroup(),
		wait:    &sync.WaitGroup{},
	}
}

// SpawnCtx runs a task and manages it.
// If the task returns an error that isn't nil nor is a cancellation,
// it registers the error and cancels the whole TaskGroup.
func (tg *TaskGroup) SpawnCtx(cb Task) {
	tg.wait.Add(1)
	go func() {
		if err := cb(tg.Context); err != nil && !IsCancellation(err) {
			tg.Errors.Add(err)
			tg.Cancel()
		}
		tg.wait.Done()
	}()
}

// Spawn runs and manages a simple callback that isn't a Task.
func (tg *TaskGroup) Spawn(cb func()) {
	tg.wait.Add(1)
	go func() {
		cb()
		tg.wait.Done()
	}()
}

// Wait blocks until all tasks in the group have returned.
func (tg *TaskGroup) Wait() *ErrorGroup {
	defer tg.Cancel()
	tg.wait.Wait()
	return tg.Errors
}

// ErrorGroup is a goroutine-safe group of errors for use by TaskGroup.
type ErrorGroup struct {
	mutex  *sync.RWMutex
	errors []error
}

// NewErrorGroup creates a new ErrorGroup.
func NewErrorGroup() *ErrorGroup {
	return &ErrorGroup{
		mutex:  &sync.RWMutex{},
		errors: []error{},
	}
}

// Add adds an error to the group, and merges with it if it also is an ErrorGroup.
func (eg *ErrorGroup) Add(err error) {
	eg.mutex.Lock()
	defer eg.mutex.Unlock()
	if errGrp, ok := err.(*ErrorGroup); ok {
		eg.errors = append(eg.errors, errGrp.Errors()...)
	} else {
		eg.errors = append(eg.errors, err)
	}
}

// Errors returns a slice of the errors it has registered so far.
func (eg *ErrorGroup) Errors() []error {
	eg.mutex.RLock()
	defer eg.mutex.RUnlock()
	errors := make([]error, len(eg.errors))
	copy(errors, eg.errors)
	return errors
}

// Len returns the number of errors it has registered so far without the need to generate a slice of errors first.
func (eg *ErrorGroup) Len() int {
	eg.mutex.RLock()
	defer eg.mutex.RUnlock()
	return len(eg.errors)
}

// Error implements the Error interface.
func (eg *ErrorGroup) Error() string {
	errors := eg.Errors()
	nbErrs := len(errors)
	if nbErrs == 0 {
		return ""
	} else if nbErrs == 1 {
		return errors[0].Error()
	}
	return fmt.Sprintf("%d error(s): %v", nbErrs, errors)
}

// ToError unwraps the ErrorGroup from its interface when nil.
func (eg *ErrorGroup) ToError() error {
	if eg.Len() == 0 {
		return nil
	}
	return eg
}

// Retrier automatically retries a Task when it fails, with exponential backoff,
// an optional limit of retries and a limit to the backoff.
// It can only be used for a single Task; misuse may result in unforeseen behavior and data races.
// To use, first run NewRetrier, then Set, and finally pass Retrier.Task to whatever expects a Task.
type Retrier struct {
	Backoff     time.Duration
	Callback    Task
	LastRestart time.Time
	MaxInterval time.Duration
	OnError     func(*Retrier, error)
	ErrorFilter func(error) bool
	ResetAfter  time.Duration
	Retries     int
	Times       int
}

// NewRetrier creates a new Retrier that has yet to be bound to a Task.
func NewRetrier(conf RetryConf, onError func(*Retrier, error)) *Retrier {
	return &Retrier{
		MaxInterval: conf.MaxInterval.Value,
		ResetAfter:  conf.ResetAfter.Value,
		Times:       conf.Times,
		OnError:     onError,
		Backoff:     time.Second,
	}
}

// Implement the Stringer interface with human-readable information about the state of the retrier.
func (r *Retrier) String() string {
	times := ""
	max_interval := ""
	if r.Times > -1 {
		times = fmt.Sprintf("/%d", r.Times)
	}
	if r.MaxInterval > 0 {
		max_interval = fmt.Sprintf("/%s", r.MaxInterval)
	}
	return fmt.Sprintf("%d%s retries with %s%s of backoff", r.Retries, times, r.Backoff, max_interval)
}

// Set binds a Retrier to a Task and returns the Retrier so that the method can be chained.
func (r *Retrier) Set(task Task) *Retrier {
	r.Callback = task
	return r
}

// SetErrorFilter binds a filter for what errors should trigger a restart, and returns the Retrier so that the method can be chained.
func (r *Retrier) SetErrorFilter(filter func(error) bool) *Retrier {
	r.ErrorFilter = filter
	return r
}

// Task wraps with retry logic the Task to which the Retrier has been bound to.
func (r *Retrier) Task(ctx context.Context) error {
	var err error
	for {
		r.LastRestart = time.Now()
		err = r.Callback(ctx)
		r.conditionalReset()
		if r.shouldRestartOn(err) {
			if r.OnError != nil {
				r.OnError(r, err)
			}
			if !r.sleepCtx(ctx) {
				return ctx.Err()
			}
			r.Retries++
		} else {
			break
		}
	}
	return err
}

func (r *Retrier) conditionalReset() {
	if r.ResetAfter > 0 && r.LastRestart.Add(r.ResetAfter).Before(time.Now()) {
		r.Retries = 0
		r.Backoff = time.Second
	}
}

func (r *Retrier) shouldRestartOn(err error) bool {
	if err == nil {
		return false
	}

	if r.Times == -1 {
		return true
	}

	if IsCancellation(err) {
		return false
	}

	if r.ErrorFilter != nil && !r.ErrorFilter(err) {
		return false
	}

	return r.Retries < r.Times
}

func (r *Retrier) sleepCtx(ctx context.Context) bool {
	var sleeptime time.Duration
	if r.MaxInterval > 0 && r.Backoff > r.MaxInterval {
		sleeptime = r.MaxInterval
	} else {
		sleeptime = r.Backoff
		r.Backoff *= 2
	}
	return SleepCtx(ctx, sleeptime)
}

// WithCtx adds a cancellation point before the execution of the given callback.
func WithCtx(cb func() error) Task {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return cb()
	}
}
