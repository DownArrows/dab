package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
)

// LevelLoggerLevels is a list of the names of the available levels of logging.
var LevelLoggerLevels = []string{"Fatal", "Error", "Info", "Debug"}

// Constants corresponding to the available levels of logging, such as the most important level is the lowest integer.
const (
	LevelLoggerFatal = iota
	LevelLoggerError
	LevelLoggerInfo
	LevelLoggerDebug
)

// LevelLogger is a logger that has several levels of severity
// and has methods for easy templating of the messages.
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

// StdLevelLogger is the standard implementation of a LevelLogger that writes to a given io.Writer.
type StdLevelLogger struct {
	out   io.Writer
	level int
}

// NewStdLevelLogger creates a new StdLevelLogger that writes to the given io.Writer messages up to the level with the given name.
// Returns an error if the name of the logging level is invalid.
func NewStdLevelLogger(out io.Writer, level string) (*StdLevelLogger, error) {

	intMap := map[string]int{
		"Fatal": LevelLoggerFatal,
		"Error": LevelLoggerError,
		"Info":  LevelLoggerInfo,
		"Debug": LevelLoggerDebug,
	}
	if _, ok := intMap[level]; !ok {
		return nil, fmt.Errorf("invalid logging level %s", level)
	}

	return &StdLevelLogger{out: out, level: intMap[level]}, nil
}

// Debug writes a debug message.
func (ll *StdLevelLogger) Debug(msg interface{}) {
	if ll.level >= LevelLoggerDebug {
		file, line := locateInSource(2)
		ll.log(LevelLoggerDebug, fmt.Sprintf("%s:%d: %s", file, line, msg))
	}
}

// Debugf writes a debug message with a template.
func (ll *StdLevelLogger) Debugf(template string, opts ...interface{}) {
	if ll.level >= LevelLoggerDebug {
		file, line := locateInSource(2)
		ll.logf(LevelLoggerDebug, file+":"+strconv.Itoa(line)+": "+template, opts...)
	}
}

// Error logs an error.
func (ll *StdLevelLogger) Error(err error) {
	ll.log(LevelLoggerError, err)
}

// Errorf logs an error with a template.
func (ll *StdLevelLogger) Errorf(template string, opts ...interface{}) {
	ll.logf(LevelLoggerError, template, opts...)
}

// Fatal logs an error and stops the application.
func (ll *StdLevelLogger) Fatal(err error) {
	ll.log(LevelLoggerFatal, err)
	os.Exit(1)
}

// Fatalf logs an error with a template and stops the application.
func (ll *StdLevelLogger) Fatalf(template string, opts ...interface{}) {
	ll.logf(LevelLoggerFatal, template, opts...)
	os.Exit(1)
}

// Info writes an info-level message.
func (ll *StdLevelLogger) Info(msg interface{}) {
	ll.log(LevelLoggerInfo, msg)
}

// Infof writes an info-level message with a template.
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
