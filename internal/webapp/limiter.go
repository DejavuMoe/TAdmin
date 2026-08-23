package webapp

import (
	"sync"
	"time"
)

type limitEntry struct {
	windowStart time.Time
	count       int
}

type fixedWindowLimiter struct {
	mu         sync.Mutex
	entries    map[string]limitEntry
	window     time.Duration
	limit      int
	maxEntries int
}

func newFixedWindowLimiter(window time.Duration, limit, maxEntries int) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		entries:    make(map[string]limitEntry),
		window:     window,
		limit:      limit,
		maxEntries: maxEntries,
	}
}

func (l *fixedWindowLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[key]
	if exists && now.Sub(entry.windowStart) >= l.window {
		delete(l.entries, key)
		exists = false
	}
	if !exists && len(l.entries) >= l.maxEntries {
		for existingKey, existing := range l.entries {
			if now.Sub(existing.windowStart) >= l.window {
				delete(l.entries, existingKey)
			}
		}
		if len(l.entries) >= l.maxEntries {
			return false
		}
	}
	if !exists {
		entry = limitEntry{windowStart: now}
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}
