package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dab := NewDownArrowsBot(os.Stderr, log.Lshortfile, os.Stdout)
	done := make(chan bool)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				dab.Logger.Print(r)
				done <- true
			}
		}()
		dab.Launch(os.Args[1:])
		done <- !dab.Daemon
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	for is_done := false; !is_done; {
		select {
		case <-sig:
			is_done = true
		case is_done = <-done:
		}
	}
	dab.Close()
}
