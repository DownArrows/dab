package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dab := NewDownArrowsBot(os.Stderr, log.Lshortfile, os.Stdout)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		done <- dab.Run(ctx, os.Args[1:])
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)

	var err error
	select {
	case err = <-done:
		cancel()
	case name := <-sig:
		fmt.Fprintf(os.Stderr, "%s received, shutting down\n", name)
		cancel()
		err = <-done
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}
