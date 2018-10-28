package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

const (
	normalShutdown = iota
	errorShutdown
	noShutdown
)

func main() {
	dab := NewDownArrowsBot(os.Stderr, log.Lshortfile, os.Stdout)
	done := make(chan int)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				dab.Logger.Print(r)
				done <- errorShutdown
			}
		}()
		dab.Launch(os.Args[1:])

		if dab.Daemon {
			done <- noShutdown
		} else {
			done <- normalShutdown
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)

	status := noShutdown
	for status == noShutdown {
		select {
		case <-sig:
			status = normalShutdown
			break
		case status = <-done:
			break
		}
	}

	dab.Close()

	if status == errorShutdown {
		os.Exit(1)
	}
}
