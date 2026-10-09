package transport

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestShownCacheAnswersFromItsCopyAndRefreshesBehind(t *testing.T) {
	c := newShownCache[int](20*time.Millisecond, time.Second, time.Second)
	var reads atomic.Int32
	read := func(context.Context) (int, error) { return int(reads.Add(1)), nil }
	ctx := context.Background()
	if v, _ := c.get(ctx, "firm|a", read); v != 1 {
		t.Fatalf("first read %d, want 1", v)
	}
	if v, _ := c.get(ctx, "firm|a", read); v != 1 || reads.Load() != 1 {
		t.Fatalf("a fresh copy is answered without a read: %d after %d reads", v, reads.Load())
	}
	time.Sleep(30 * time.Millisecond)
	// Past fresh: the copy is answered at once and read again behind.
	if v, _ := c.get(ctx, "firm|a", read); v != 1 {
		t.Fatalf("a stale copy is still answered at once, got %d", v)
	}
	deadline := time.Now().Add(time.Second)
	for reads.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)
	if v, _ := c.get(ctx, "firm|a", read); v != 2 {
		t.Fatalf("the background read replaces the copy, got %d", v)
	}
	c.forget("firm")
	if v, _ := c.get(ctx, "firm|a", read); v != 3 {
		t.Fatalf("after forget the provider is read again, got %d", v)
	}
}

func TestShownCacheKeepsAForgetAgainstAnOlderRead(t *testing.T) {
	c := newShownCache[string](time.Minute, time.Minute, time.Minute)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan string)
	go func() {
		v, _ := c.get(context.Background(), "firm|x", func(context.Context) (string, error) {
			close(started)
			<-release
			return "before the change", nil
		})
		done <- v
	}()
	<-started
	c.forget("firm")
	close(release)
	<-done
	if v, _ := c.get(context.Background(), "firm|x", func(context.Context) (string, error) { return "after the change", nil }); v != "after the change" {
		t.Fatalf("a read that began before a forget must not be kept, got %q", v)
	}
}

func TestShownCacheEvictsPastItsKeepingTime(t *testing.T) {
	c := newShownCache[int](time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	c.get(context.Background(), "a", func(context.Context) (int, error) { return 1, nil })
	time.Sleep(20 * time.Millisecond)
	c.get(context.Background(), "b", func(context.Context) (int, error) { return 2, nil })
	c.mu.Lock()
	_, kept := c.entries["a"]
	c.mu.Unlock()
	if kept {
		t.Fatal("a copy past its keeping time leaves")
	}
}

func TestShownCacheShowsAProviderFailureRatherThanAnOldCopy(t *testing.T) {
	c := newShownCache[int](5*time.Millisecond, 50*time.Millisecond, time.Second)
	ctx := context.Background()
	c.get(ctx, "a", func(context.Context) (int, error) { return 1, nil })
	failing := func(context.Context) (int, error) { return 0, context.DeadlineExceeded }
	time.Sleep(10 * time.Millisecond)
	// Stale but within served: answered, and its background read fails.
	if v, err := c.get(ctx, "a", failing); err != nil || v != 1 {
		t.Fatalf("a copy within its serving time is answered, got %d %v", v, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		failed := c.failed["a"]
		c.mu.Unlock()
		if failed || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := c.get(ctx, "a", failing); err == nil {
		t.Fatal("after a failed refresh the provider's failure shows")
	}
	c.get(ctx, "b", func(context.Context) (int, error) { return 2, nil })
	time.Sleep(60 * time.Millisecond)
	if _, err := c.get(ctx, "b", failing); err == nil {
		t.Fatal("a copy past its serving time is not answered when the provider fails")
	}
}
