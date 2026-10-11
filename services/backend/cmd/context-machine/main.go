package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"close.local/backend/internal/llm/machine"
)

func main() {
	// The hosted service listens on :8080; a local evaluation binds a loopback address (scripts/local-context-machine.sh).
	addr := flag.String("addr", ":8080", "the address to listen on")
	flag.Parse()
	handler, err := machine.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
