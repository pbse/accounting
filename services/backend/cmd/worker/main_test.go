package main

import (
	"context"
	"testing"
	"time"
)

func TestSuperviseRestartsAfterPanicOnly(t *testing.T) {
	restartAfter = time.Millisecond
	runs, panics := 0, []string{}
	supervise(context.Background(), "demo", func() {
		runs++
		if runs == 1 {
			panic("boom")
		}
	}, func(name string, p any, stack []byte) {
		if len(stack) == 0 {
			t.Error("stack was not captured at the panic")
		}
		panics = append(panics, name+": "+p.(string))
	})
	if runs != 2 || len(panics) != 1 || panics[0] != "demo: boom" {
		t.Fatalf("runs=%d panics=%v", runs, panics)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runs = 0
	supervise(ctx, "stopping", func() { runs++; panic("again") }, func(string, any, []byte) {})
	if runs != 1 {
		t.Fatalf("a cancelled worker restarted its loop: runs=%d", runs)
	}
}
