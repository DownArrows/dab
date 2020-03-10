package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Go read dab.go and the definition of DownArrowsBot to
	// understand the structure of the whole application.
	dab := NewDownArrowsBot(os.Stderr, os.Stdout)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		done <- dab.Run(ctx, os.Args[1:])
	}()

	kill := make(chan os.Signal, 1)
	signal.Notify(kill, syscall.SIGTERM, os.Interrupt, os.Kill)

	var err error
	select {
	case err = <-done:
		cancel()
	case <-kill:
		cancel()
		err = <-done
	}

	if err != nil && !IsCancellation(err) {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}
