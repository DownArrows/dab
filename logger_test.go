package main

import (
	"testing"
)

// Logger for tests that shows no output unless there's an error.
type TestLevelLogger struct {
	t *testing.T
}

func NewTestLevelLogger(t *testing.T) LevelLogger {
	return TestLevelLogger{t: t}
}

func (tl TestLevelLogger) Debug(msg interface{}) {
	tl.t.Log(msg)
}

func (tl TestLevelLogger) Debugf(template string, msg ...interface{}) {
	tl.t.Logf(template, msg...)
}

func (tl TestLevelLogger) Debugd(cb func() interface{}) {
	tl.t.Log(cb())
}

func (tl TestLevelLogger) Error(err error) {
	tl.t.Error(err)
}

func (tl TestLevelLogger) Errorf(template string, msg ...interface{}) {
	tl.t.Errorf(template, msg...)
}

func (tl TestLevelLogger) Errord(cb func() error) {
	tl.t.Error(cb())
}

func (tl TestLevelLogger) Fatal(err error) {
	tl.t.Fatal(err)
}

func (tl TestLevelLogger) Fatalf(template string, msg ...interface{}) {
	tl.t.Fatalf(template, msg...)
}

func (tl TestLevelLogger) Info(msg interface{}) {
	tl.t.Log(msg)
}

func (tl TestLevelLogger) Infof(template string, msg ...interface{}) {
	tl.t.Logf(template, msg...)
}

func (tl TestLevelLogger) Infod(cb func() interface{}) {
	tl.t.Log(cb())
}
