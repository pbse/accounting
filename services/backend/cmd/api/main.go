package main

import (
	"close.local/backend/internal/transport"
	_ "close.local/backend/workflows" // the workflow catalog, for the admin panel and starting runs
	"log"
)

func main() {
	handler, close, err := transport.ConfiguredAPI()
	if err != nil {
		log.Fatal(err)
	}
	defer close()
	transport.Run(handler)
}
