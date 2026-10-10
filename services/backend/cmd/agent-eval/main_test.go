package main

import "testing"

func TestContextMachineReplayRequiresLocalService(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:8090/v1/run", "http://localhost:8090/v1/run", "http://[::1]:8090/v1/run"} {
		if err := localContextMachine(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"", "http://context-machine.internal/v1/run", "https://prod.example/v1/run", "http://localhost/v1/complete", "http://user:password@localhost/v1/run", "http://localhost/v1/run?x=y"} {
		if err := localContextMachine(endpoint); err == nil {
			t.Fatal(endpoint)
		}
	}
}
