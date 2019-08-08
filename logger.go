package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
)

var LevelLoggerLevels = []string{"Fatal", "Error", "Info", "Debug"}

const (
	LevelLoggerFatal = iota
	LevelLoggerError
	LevelLoggerInfo
	LevelLoggerDebug
)

type LevelLogger interface {
	Debugf(string, ...interface{})
	Debug(interface{})
	Error(error)
	Errorf(string, ...interface{})
	Fatal(error)
	Fatalf(string, ...interface{})
	Infof(string, ...interface{})
	Info(interface{})
}

type StdLevelLogger struct {
	out   io.Writer
	level int
}

func NewStdLevelLogger(out io.Writer, level string) (*StdLevelLogger, error) {

	int_map := map[string]int{
		"Fatal": LevelLoggerFatal,
		"Error": LevelLoggerError,
		"Info":  LevelLoggerInfo,
		"Debug": LevelLoggerDebug,
	}
	if _, ok := int_map[level]; !ok {
		return nil, fmt.Errorf("invalid logging level %s", level)
	}

	return &StdLevelLogger{out: out, level: int_map[level]}, nil
}

func (ll *StdLevelLogger) Debug(msg interface{}) {
	if ll.level >= LevelLoggerDebug {
		file, line := locateInSource(2)
		ll.log(LevelLoggerDebug, fmt.Sprintf("%s:%d: %s", file, line, msg))
	}
}

func (ll *StdLevelLogger) Debugf(template string, opts ...interface{}) {
	if ll.level >= LevelLoggerDebug {
		file, line := locateInSource(2)
		ll.logf(LevelLoggerDebug, file+":"+strconv.Itoa(line)+": "+template, opts...)
	}
}

func (ll *StdLevelLogger) Error(err error) {
	ll.log(LevelLoggerError, err)
}

func (ll *StdLevelLogger) Errorf(template string, opts ...interface{}) {
	ll.logf(LevelLoggerError, template, opts...)
}

func (ll *StdLevelLogger) Fatal(err error) {
	ll.log(LevelLoggerFatal, err)
	os.Exit(1)
}

func (ll *StdLevelLogger) Fatalf(template string, opts ...interface{}) {
	ll.logf(LevelLoggerFatal, template, opts...)
	os.Exit(1)
}

func (ll *StdLevelLogger) Info(msg interface{}) {
	ll.log(LevelLoggerInfo, msg)
}

func (ll *StdLevelLogger) Infof(template string, opts ...interface{}) {
	ll.logf(LevelLoggerInfo, template, opts...)
}

func (ll *StdLevelLogger) log(level int, msg interface{}) {
	if ll.level >= level {
		if _, err := fmt.Fprintf(ll.out, "%s\n", msg); err != nil {
			panic(err)
		}
	}
}

func (ll *StdLevelLogger) logf(level int, template string, opts ...interface{}) {
	if ll.level >= level {
		if _, err := fmt.Fprintf(ll.out, template+"\n", opts...); err != nil {
			panic(err)
		}
	}
}

func locateInSource(depth uint) (string, int) {
	if _, file, line, ok := runtime.Caller(int(depth)); ok {
		return path.Base(file), line
	}
	return "", 0
}
