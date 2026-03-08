package server

import (
	"testing"
	"time"
)

func TestSrvCacheGetEmpty(t *testing.T) {
	c := &srvCache{}

	urls, fresh, stale := c.get()
	if urls != nil {
		t.Errorf("expected nil urls, got %v", urls)
	}
	if fresh {
		t.Error("expected fresh=false on empty cache")
	}
	if stale {
		t.Error("expected stale=false on empty cache")
	}
}

func TestSrvCacheGetFresh(t *testing.T) {
	c := &srvCache{}
	c.set([]string{"http://host1:9093/api/v2/alerts"}, time.Minute)

	urls, fresh, stale := c.get()
	if !fresh {
		t.Error("expected fresh=true after set")
	}
	if stale {
		t.Error("expected stale=false when fresh")
	}
	if len(urls) != 1 || urls[0] != "http://host1:9093/api/v2/alerts" {
		t.Errorf("unexpected urls: %v", urls)
	}
}

func TestSrvCacheGetStale(t *testing.T) {
	c := &srvCache{}

	now := time.Now()
	c.mu.Lock()
	c.urls = []string{"http://host1:9093/api/v2/alerts"}
	c.expiresAt = now.Add(-time.Second)  // already expired
	c.staleUntil = now.Add(time.Minute)  // still within stale window
	c.mu.Unlock()

	urls, fresh, stale := c.get()
	if fresh {
		t.Error("expected fresh=false for expired cache")
	}
	if !stale {
		t.Error("expected stale=true within stale window")
	}
	if len(urls) != 1 || urls[0] != "http://host1:9093/api/v2/alerts" {
		t.Errorf("unexpected urls: %v", urls)
	}
}

func TestSrvCacheGetExpired(t *testing.T) {
	c := &srvCache{}

	now := time.Now()
	c.mu.Lock()
	c.urls = []string{"http://host1:9093/api/v2/alerts"}
	c.expiresAt = now.Add(-time.Minute * 2)  // expired
	c.staleUntil = now.Add(-time.Minute)     // stale window also expired
	c.mu.Unlock()

	urls, fresh, stale := c.get()
	if fresh {
		t.Error("expected fresh=false for expired cache")
	}
	if stale {
		t.Error("expected stale=false beyond stale window")
	}
	if urls != nil {
		t.Errorf("expected nil urls beyond stale window, got %v", urls)
	}
}

func TestSrvCacheSetUpdatesTimes(t *testing.T) {
	c := &srvCache{}
	ttl := time.Minute

	before := time.Now()
	c.set([]string{"http://host1:9093/api/v2/alerts"}, ttl)
	after := time.Now()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.expiresAt.Before(before.Add(ttl)) || c.expiresAt.After(after.Add(ttl)) {
		t.Errorf("expiresAt out of expected range: %v", c.expiresAt)
	}
	if c.staleUntil.Before(before.Add(ttl*2)) || c.staleUntil.After(after.Add(ttl*2)) {
		t.Errorf("staleUntil out of expected range: %v", c.staleUntil)
	}
}

func TestSrvCacheSetClearsResolving(t *testing.T) {
	c := &srvCache{}
	c.mu.Lock()
	c.resolving = true
	c.mu.Unlock()

	c.set([]string{"http://host1:9093/api/v2/alerts"}, time.Minute)

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.resolving {
		t.Error("expected resolving=false after set")
	}
}

func TestSrvCacheMarkResolving(t *testing.T) {
	c := &srvCache{}

	if !c.markResolving() {
		t.Error("expected markResolving to return true on first call")
	}

	c.mu.RLock()
	if !c.resolving {
		t.Error("expected resolving=true after markResolving")
	}
	c.mu.RUnlock()

	if c.markResolving() {
		t.Error("expected markResolving to return false when already resolving")
	}
}

func TestSrvCacheClearResolving(t *testing.T) {
	c := &srvCache{}
	c.mu.Lock()
	c.resolving = true
	c.mu.Unlock()

	c.clearResolving()

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.resolving {
		t.Error("expected resolving=false after clearResolving")
	}
}
