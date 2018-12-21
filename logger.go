package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
)

var LevelLoggerLevels = []string{"Error", "Info", "Debug"}

const (
	levelLoggerError = iota
	levelLoggerInfo
	levelLoggerDebug
)

func locateInSource(depth uint) (string, int) {
	if _, file, line, ok := runtime.Caller(int(depth)); ok {
		return path.Base(file), line
	}
	return "", 0
}

type LevelLogger struct {
	out   io.Writer
	level int
}

func NewLevelLogger(out io.Writer, level string) (LevelLogger, error) {
	ll := LevelLogger{out: out}

	int_map := map[string]int{
		"Error": levelLoggerError,
		"Info":  levelLoggerInfo,
		"Debug": levelLoggerDebug,
	}
	if _, ok := int_map[level]; !ok {
		return ll, fmt.Errorf("invalid logging level %s", level)
	}

	ll.level = int_map[level]
	return ll, nil
}

func (ll LevelLogger) log(level int, msg interface{}) {
	if ll.level >= level {
		if _, err := fmt.Fprintf(ll.out, "%s\n", msg); err != nil {
			panic(err)
		}
	}
}

func (ll LevelLogger) logf(level int, template string, opts ...interface{}) {
	if ll.level >= level {
		if _, err := fmt.Fprintf(ll.out, template+"\n", opts...); err != nil {
			panic(err)
		}
	}
}

func (ll LevelLogger) Fatal(err error) {
	ll.log(-1, err)
	os.Exit(1)
}

func (ll LevelLogger) Fatalf(template string, opts ...interface{}) {
	ll.logf(-1, template, opts...)
	os.Exit(1)
}

func (ll LevelLogger) Error(err error) {
	ll.log(levelLoggerError, err)
}

func (ll LevelLogger) Errorf(template string, opts ...interface{}) {
	ll.logf(levelLoggerError, template, opts...)
}

func (ll LevelLogger) Info(msg interface{}) {
	ll.log(levelLoggerInfo, msg)
}

func (ll LevelLogger) Infof(template string, opts ...interface{}) {
	ll.logf(levelLoggerInfo, template, opts...)
}

func (ll LevelLogger) Debug(msg interface{}) {
	if ll.level >= levelLoggerDebug {
		file, line := locateInSource(2)
		ll.log(levelLoggerDebug, fmt.Sprintf("%s:%d: %s", file, line, msg))
	}
}

func (ll LevelLogger) Debugf(template string, opts ...interface{}) {
	if ll.level >= levelLoggerDebug {
		file, line := locateInSource(2)
		ll.logf(levelLoggerDebug, file+":"+strconv.Itoa(line)+": "+template, opts...)
	}
}
