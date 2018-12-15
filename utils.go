package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type version [3]byte

func (v version) Major() uint8 {
	return v[0]
}

func (v version) Minor() uint8 {
	return v[1]
}

func (v version) BugFix() uint8 {
	return v[2]
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.BugFix())
}

// Encodes the version on the lower three bytes of a 32 bits integer.
func (v version) ToInt32() int32 {
	return (int32(v.Major()) * 65536) + (int32(v.Minor()) * 256) + int32(v.BugFix())
}

func versionFromInt32(encoded int32) version {
	major := encoded / 65536

	encoded %= 65536
	minor := encoded / 256

	bugfix := encoded % 256

	return version{byte(major), byte(minor), byte(bugfix)}
}

func isCancellation(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func sleepCtx(ctx context.Context, duration time.Duration) bool {
	select {
	case <-time.After(duration):
		return true
	case <-ctx.Done():
		return false
	}
}

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

func (tg *TaskGroup) Spawn(cb func(context.Context) error) {
	tg.wait.Add(1)
	go func() {
		if err := cb(tg.context); err != nil && !isCancellation(err) {
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

func autopanic(err error) {
	if err != nil {
		panic(err)
	}
}

func fileOlderThan(path string, max_age time.Duration) (bool, error) {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	time_diff := time.Now().Sub(stat.ModTime())
	return (time_diff > max_age), nil
}

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
