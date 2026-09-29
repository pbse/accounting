package main

import (
	"log"
	"net/http"
	"time"

	"close.local/backend/internal/llm/machine"
)

func main() {
	handler, err := machine.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
