package main

import (
	"close.local/backend/internal/transport"
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
