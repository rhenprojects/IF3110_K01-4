package userhandler

import (
	"sync"
	"time"
)

const (
	defaultLoginMaxFailures   = 5
	defaultLoginIPMaxFailures = 100
	defaultLoginWindow        = 15 * time.Minute
	loginLimiterMaxEntries    = 10000
)

type loginLimitEntry struct {
	failures int
	expires  time.Time
}

// loginLimiter membatasi percobaan login yang gagal per username+IP.
// State disimpan di proses; cukup untuk satu instance backend.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]loginLimitEntry
	max     int
	window  time.Duration
	now     func() time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		entries: make(map[string]loginLimitEntry),
		max:     max,
		window:  window,
		now:     time.Now,
	}
}

func (l *loginLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry, ok := l.entries[key]
	if !ok || now.After(entry.expires) {
		return true, 0
	}
	if entry.failures >= l.max {
		return false, entry.expires.Sub(now)
	}
	return true, 0
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.entries) > loginLimiterMaxEntries {
		for k, v := range l.entries {
			if now.After(v.expires) {
				delete(l.entries, k)
			}
		}
	}
	entry, ok := l.entries[key]
	if !ok || now.After(entry.expires) {
		entry = loginLimitEntry{expires: now.Add(l.window)}
	}
	entry.failures++
	l.entries[key] = entry
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
