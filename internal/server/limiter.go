package server

import (
	"sync"
	"time"
)

// limiter erlaubt höchstens n Ereignisse je Schlüssel innerhalb des Zeitfensters.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	win  map[string]time.Duration
}

func newLimiter() *limiter {
	return &limiter{hits: map[string][]time.Time{}, win: map[string]time.Duration{}}
}

func (l *limiter) allow(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			h = append(h, t)
		}
	}
	l.win[key] = window
	if len(h) >= n {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	delete(l.win, key)
	l.mu.Unlock()
}

func (l *limiter) gc() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, h := range l.hits {
		if len(h) == 0 || now.Sub(h[len(h)-1]) > l.win[k] {
			delete(l.hits, k)
			delete(l.win, k)
		}
	}
}
