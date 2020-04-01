package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
)

var (
	// LevelLoggerLevels is a list of the names of the available levels of logging,
	// ordered from the most important to the least.
	LevelLoggerLevels = []string{"Fatal", "Error", "Info", "Debug"}
	// LevelLoggerPriority is a map from the names of the logging levels to their priority.
	LevelLoggerPriority = makeLevelLoggerPriorities()
)

func makeLevelLoggerPriorities() map[string]int {
	priorities := make(map[string]int)
	for priority, level := range LevelLoggerLevels {
		priorities[level] = priority
	}
	return priorities
}

// LevelLogger is a logger that has several levels of severity, -f methods for easy
// templating of the messages, and -d methods for deferred evaluation of the messages.
type LevelLogger interface {
	Debug(Any)
	Debugd(func() Any)
	Debugf(string, ...Any)
	Error(error)
	Errord(func() error)
	Errorf(string, ...Any)
	Fatal(error)
	Fatalf(string, ...Any)
	Info(Any)
	Infod(func() Any)
	Infof(string, ...Any)
}

// StdLevelLogger is the standard implementation of a LevelLogger that writes to a given io.Writer.
type StdLevelLogger struct {
	out   io.Writer
	level int
	tag   string
}

// NewStdLevelLogger creates a new StdLevelLogger that writes to the given io.Writer messages up to the level with the given name.
// Returns an error if the name of the logging level is invalid.
func NewStdLevelLogger(name string, out io.Writer, level string) (*StdLevelLogger, error) {
	level = strings.Title(strings.ToLower(level))
	priority, exists := LevelLoggerPriority[level]
	if !exists {
		return nil, fmt.Errorf("invalid logging level %q", level)
	}
	return &StdLevelLogger{out: out, level: priority, tag: "[" + name + "] "}, nil
}

// Debug writes a debug message.
func (ll *StdLevelLogger) Debug(msg Any) {
	if ll.include("Debug") {
		ll.logDebug(msg)
	}
}

// Debugf writes a debug message with a template.
func (ll *StdLevelLogger) Debugf(template string, opts ...Any) {
	if ll.include("Debug") {
		ll.logDebug(fmt.Sprintf(template, opts...))
	}
}

// Debugd writes a deferred debug message.
func (ll *StdLevelLogger) Debugd(cb func() Any) {
	if ll.include("Debug") {
		ll.logDebug(cb())
	}
}

func (ll *StdLevelLogger) logDebug(msg Any) {
	file, line := locateInSource(3)
	ll.log("Debug", fmt.Sprintf("%s:%d: %s", file, line, msg))
}

// Error logs an error.
func (ll *StdLevelLogger) Error(err error) {
	ll.logLevel("Error", err)
}

// Errorf logs an error with a template.
func (ll *StdLevelLogger) Errorf(template string, opts ...Any) {
	ll.logLevel("Error", fmt.Sprintf(template, opts...))
}

// Errord logs a deferred error.
func (ll *StdLevelLogger) Errord(cb func() error) {
	if ll.include("Error") {
		ll.log("Error", cb())
	}
}

// Fatal logs an error and stops the application.
func (ll *StdLevelLogger) Fatal(err error) {
	ll.logLevel("Fatal", err)
	os.Exit(1)
}

// Fatalf logs an error with a template and stops the application.
func (ll *StdLevelLogger) Fatalf(template string, opts ...Any) {
	ll.logLevel("Fatal", fmt.Sprintf(template, opts...))
	os.Exit(1)
}

// Info writes an info-level message.
func (ll *StdLevelLogger) Info(msg Any) {
	ll.logLevel("Info", msg)
}

// Infof writes an info-level message with a template.
func (ll *StdLevelLogger) Infof(template string, opts ...Any) {
	ll.logLevel("Info", fmt.Sprintf(template, opts...))
}

// Infod writes a deferred info-level message.
func (ll *StdLevelLogger) Infod(cb func() Any) {
	if ll.include("Info") {
		ll.log("Info", cb())
	}
}

func (ll *StdLevelLogger) include(level string) bool {
	priority, exists := LevelLoggerPriority[level]
	if !exists {
		panic(fmt.Sprintf("invalid logging level %s; this is a mistake in the source code", level))
	}
	return ll.level >= priority
}

func (ll *StdLevelLogger) logLevel(level string, msg Any) {
	if ll.include(level) {
		ll.log(level, msg)
	}
}

func (ll *StdLevelLogger) log(level string, msg Any) {
	if _, err := fmt.Fprintf(ll.out, "%s%s: %s\n", ll.tag, level, msg); err != nil {
		panic(err)
	}
}

func locateInSource(depth uint) (string, int) {
	if _, file, line, ok := runtime.Caller(int(depth)); ok {
		return path.Base(file), line
	}
	return "", 0
}
