package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alicebob/miniredis/v2"
)

func main() {
	addr := "127.0.0.1:6379"
	if v := os.Getenv("MINIREDIS_ADDR"); v != "" {
		addr = v
	}
	m := miniredis.NewMiniRedis()
	if err := m.StartAddr(addr); err != nil {
		fmt.Fprintf(os.Stderr, "miniredis start failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("miniredis listening on %s\n", m.Addr())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	m.Close()
}
