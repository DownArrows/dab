package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dab := NewDownArrowsBot(os.Stderr, log.Lshortfile, os.Stdout)
	defer dab.Close()
	dab.Launch(os.Args[1:])
	if dab.Daemon {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
		<-sig
	}
}
