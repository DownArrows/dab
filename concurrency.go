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

// Launches and shuts down a group of goroutine which take a context and return an error.
// Use TaskGroup.Spawn to launch functions asynchronously,
// and once you're done use TaskGroup.Wait to wait on them.
// To cancel a TaskGroup, use TaskGroup.Cancel, or cancel the context you passed
// when creating the TaskGroup.
// TaskGroup.Wait returns a type of error that contains multiple errors and which
// can be converted to a normal error that can be usefully compared to nil.
type TaskGroup struct {
	cancel  context.CancelFunc
	context context.Context
	errors  *ErrorGroup
	wait    *sync.WaitGroup
}

func NewTaskGroup(parent context.Context) *TaskGroup {
	ctx, cancel := context.WithCancel(parent)
	return &TaskGroup{
		cancel:  cancel,
		context: ctx,
		errors:  NewErrorGroup(),
		wait:    &sync.WaitGroup{},
	}
}

func (tg *TaskGroup) Spawn(cb Task) {
	tg.wait.Add(1)
	go func() {
		if err := cb(tg.context); err != nil && !IsCancellation(err) {
			tg.errors.Add(err)
			tg.Cancel()
		}
		tg.wait.Done()
	}()
}

func (tg *TaskGroup) Cancel() {
	tg.cancel()
}

func (tg *TaskGroup) Wait() *ErrorGroup {
	defer tg.Cancel()
	tg.wait.Wait()
	return tg.errors
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

// Set of strings that can be used from multiple goroutines.
type SyncSet struct {
	sync.RWMutex
	data map[string]bool
}

func NewSyncSet() *SyncSet {
	return &SyncSet{
		data: make(map[string]bool),
	}
}

func (s *SyncSet) Has(key string) bool {
	s.RLock()
	_, ok := s.data[key]
	s.RUnlock()
	return ok
}

func (s *SyncSet) Put(key string) {
	s.Lock()
	s.data[key] = true
	s.Unlock()
}

func (s *SyncSet) MultiPut(keys []string) {
	s.Lock()
	for _, key := range keys {
		s.data[key] = true
	}
	s.Unlock()
}

func (s *SyncSet) Transaction(cb func(map[string]bool)) {
	s.Lock()
	defer s.Unlock()
	cb(s.data)
}

func (s *SyncSet) Len() int {
	s.RLock()
	defer s.RUnlock()
	return len(s.data)
}
