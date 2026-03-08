package server

import (
	"sync"
	"time"
)

type srvCache struct {
	mu         sync.RWMutex
	urls       []string
	expiresAt  time.Time
	staleUntil time.Time
	resolving  bool
}

func (c *srvCache) get() (urls []string, fresh bool, stale bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	if now.Before(c.expiresAt) {
		return c.urls, true, false
	}
	if now.Before(c.staleUntil) {
		return c.urls, false, true
	}
	return nil, false, false
}

func (c *srvCache) set(urls []string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.urls = urls
	c.expiresAt = now.Add(ttl)
	c.staleUntil = now.Add(ttl * 2)
	c.resolving = false
}

func (c *srvCache) markResolving() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.resolving {
		return false
	}
	c.resolving = true
	return true
}

func (c *srvCache) clearResolving() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.resolving = false
}
