package transport

import (
	"context"
	"strings"
	"sync"
	"time"
)

// shownCache keeps what a page only shows from a slow provider (WorkOS). A copy younger than fresh is answered at once;
// one older is still answered at once while it is younger than served, and read again in the background, so pages stay
// fast and at most about fresh behind. A copy older than served, or one whose background read failed, is not answered:
// the provider is read and its failure shows, so a provider outage is never hidden behind an old copy. Anything that
// decides reads the provider itself. forget drops a key's copies at once, and a read that began before a forget never
// puts its older answer back. Copies leave after kept.
type shownCache[T any] struct {
	fresh, served, kept time.Duration
	mu                  sync.Mutex
	entries             map[string]shownEntry[T]
	forgot              map[string]time.Time
	reading             map[string]bool
	failed              map[string]bool
}

type shownEntry[T any] struct {
	value T
	at    time.Time
}

func newShownCache[T any](fresh, served, kept time.Duration) *shownCache[T] {
	return &shownCache[T]{fresh: fresh, served: served, kept: kept, entries: map[string]shownEntry[T]{}, forgot: map[string]time.Time{}, reading: map[string]bool{}, failed: map[string]bool{}}
}

// get answers key from the copy when it has one young enough, and otherwise reads it with read.
func (c *shownCache[T]) get(ctx context.Context, key string, read func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	c.evict()
	if entry, ok := c.entries[key]; ok && time.Since(entry.at) < c.served && !c.failed[key] {
		if time.Since(entry.at) > c.fresh && !c.reading[key] {
			c.reading[key] = true
			// The read again outlives this request, so it takes no cancellation from it.
			go func() {
				began := time.Now()
				_, err := c.read(context.WithoutCancel(ctx), key, read)
				c.mu.Lock()
				delete(c.reading, key)
				// A failure counts only against the copy it was refreshing, not one a newer read has put since.
				if kept, ok := c.entries[key]; err != nil && ok && kept.at.Before(began) {
					c.failed[key] = true
				}
				c.mu.Unlock()
			}()
		}
		c.mu.Unlock()
		return entry.value, nil
	}
	c.mu.Unlock()
	return c.read(ctx, key, read)
}

func (c *shownCache[T]) read(ctx context.Context, key string, read func(context.Context) (T, error)) (T, error) {
	started := time.Now()
	value, err := read(ctx)
	if err != nil {
		return value, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failed, key)
	for prefix, forgot := range c.forgot {
		if strings.HasPrefix(key, prefix) && forgot.After(started) {
			return value, nil
		}
	}
	if kept, ok := c.entries[key]; !ok || kept.at.Before(started) {
		c.entries[key] = shownEntry[T]{value, started}
	}
	return value, nil
}

// forget drops every copy whose key starts with prefix (a firm's ID, say), so the next read asks the provider.
func (c *shownCache[T]) forget(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
			delete(c.failed, key)
			c.forgot[key] = now
		}
	}
	c.forgot[prefix] = now
}

// evict drops copies past their keeping time, with their failure marks, and forget marks older than it; c.mu is held.
func (c *shownCache[T]) evict() {
	for key, entry := range c.entries {
		if time.Since(entry.at) >= c.kept {
			delete(c.entries, key)
		}
	}
	for key := range c.failed {
		if _, ok := c.entries[key]; !ok {
			delete(c.failed, key)
		}
	}
	for key, at := range c.forgot {
		if time.Since(at) >= c.kept {
			delete(c.forgot, key)
		}
	}
}
