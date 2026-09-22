package main

import (
	"close.local/backend/internal/scanning"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	key := os.Getenv("UPLOAD_SCAN_KEY")
	if len(key) < 32 {
		log.Fatal("UPLOAD_SCAN_KEY is required")
	}
	database := os.Getenv("CLAMAV_DATABASE_DIR")
	if database == "" {
		database = "/tmp/close-clamav"
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = ":8080"
	}
	server := http.Server{Addr: address, Handler: scanning.Handler(key, database), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 4 * time.Minute, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
