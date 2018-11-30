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
		cancel()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)

	var err error
	select {
	case <-sig:
		fmt.Fprint(os.Stderr, "signal received, shutting down\n")
		cancel()
		err = <-done
		break
	case err = <-done:
		break
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}
