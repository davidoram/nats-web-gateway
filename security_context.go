package natswebgateway

import (
	"errors"
	"sync"
	"time"

	"github.com/davidoram/nats-web-gateway/internal/credentials"
	"github.com/nats-io/nats.go"
)

var errSecurityContextLimit = errors.New("security context connection limit reached")

type securityContextPool struct {
	mu       sync.Mutex
	entries  map[[32]byte]*securityContextEntry
	config   SecurityContext
	urls     string
	connect  connectFunc
	base     []nats.Option
	stopping bool
}

type securityContextEntry struct {
	connection natsConnection
	tracker    *permissionTracker
	created    time.Time
	lastUsed   time.Time
	references int
	expired    bool
	timer      *time.Timer
}

type securityContextLease struct {
	connection natsConnection
	tracker    *permissionTracker
	release    func()
}

func newSecurityContextPool(config SecurityContext, urls string, connect connectFunc, base []nats.Option) *securityContextPool {
	return &securityContextPool{
		entries: make(map[[32]byte]*securityContextEntry), config: config,
		urls: urls, connect: connect, base: base,
	}
}

func (pool *securityContextPool) acquire(adapted credentials.Context) (*securityContextLease, error) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.stopping {
		return nil, nats.ErrConnectionClosed
	}
	now := time.Now()
	if entry := pool.entries[adapted.Key]; entry != nil {
		if entry.expired || now.Sub(entry.created) >= time.Duration(pool.config.MaxLifetime) {
			entry.expired = true
			if entry.references == 0 {
				pool.removeLocked(adapted.Key, entry)
			} else {
				return nil, nats.ErrConnectionReconnecting
			}
		} else if !entry.connection.IsConnected() {
			return nil, nats.ErrConnectionReconnecting
		} else {
			entry.references++
			entry.lastUsed = now
			pool.scheduleLocked(adapted.Key, entry)
			return pool.leaseLocked(adapted.Key, entry), nil
		}
	}
	if len(pool.entries) >= pool.config.MaxConnections {
		pool.evictIdleLocked(now)
	}
	if len(pool.entries) >= pool.config.MaxConnections {
		return nil, errSecurityContextLimit
	}
	tracker := newPermissionTracker()
	options := append([]nats.Option{}, pool.base...)
	options = append(options,
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { tracker.handle(err) }),
	)
	options = append(options, adapted.Options...)
	connection, err := pool.connect(pool.urls, options...)
	if err != nil {
		return nil, err
	}
	entry := &securityContextEntry{connection: connection, tracker: tracker, created: now, lastUsed: now, references: 1}
	pool.entries[adapted.Key] = entry
	pool.scheduleLocked(adapted.Key, entry)
	return pool.leaseLocked(adapted.Key, entry), nil
}

func (pool *securityContextPool) leaseLocked(key [32]byte, entry *securityContextEntry) *securityContextLease {
	var once sync.Once
	return &securityContextLease{connection: entry.connection, tracker: entry.tracker, release: func() {
		once.Do(func() { pool.release(key, entry) })
	}}
}

func (pool *securityContextPool) release(key [32]byte, entry *securityContextEntry) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.entries[key] != entry {
		return
	}
	entry.references--
	entry.lastUsed = time.Now()
	if entry.references == 0 && (pool.stopping || entry.expired || time.Since(entry.created) >= time.Duration(pool.config.MaxLifetime)) {
		pool.removeLocked(key, entry)
		return
	}
	pool.scheduleLocked(key, entry)
}

func (pool *securityContextPool) scheduleLocked(key [32]byte, entry *securityContextEntry) {
	if entry.timer != nil {
		entry.timer.Stop()
	}
	if entry.expired {
		entry.timer = nil
		return
	}
	untilLifetime := time.Until(entry.created.Add(time.Duration(pool.config.MaxLifetime)))
	delay := untilLifetime
	if entry.references == 0 {
		untilIdle := time.Until(entry.lastUsed.Add(time.Duration(pool.config.IdleTimeout)))
		if untilIdle < delay {
			delay = untilIdle
		}
	}
	if delay < 0 {
		delay = 0
	}
	entry.timer = time.AfterFunc(delay, func() { pool.expire(key, entry) })
}

func (pool *securityContextPool) expire(key [32]byte, entry *securityContextEntry) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.entries[key] != entry {
		return
	}
	now := time.Now()
	lifetimeExpired := now.Sub(entry.created) >= time.Duration(pool.config.MaxLifetime)
	idleExpired := entry.references == 0 && now.Sub(entry.lastUsed) >= time.Duration(pool.config.IdleTimeout)
	if lifetimeExpired {
		entry.expired = true
	}
	if entry.references == 0 && (lifetimeExpired || idleExpired) {
		pool.removeLocked(key, entry)
		return
	}
	if entry.expired {
		entry.timer = nil
		return
	}
	pool.scheduleLocked(key, entry)
}

func (pool *securityContextPool) evictIdleLocked(now time.Time) {
	for key, entry := range pool.entries {
		if entry.references == 0 && (entry.expired || now.Sub(entry.lastUsed) >= time.Duration(pool.config.IdleTimeout)) {
			pool.removeLocked(key, entry)
		}
	}
}

func (pool *securityContextPool) removeLocked(key [32]byte, entry *securityContextEntry) {
	delete(pool.entries, key)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.connection.Close()
}

func (pool *securityContextPool) close() {
	pool.mu.Lock()
	pool.stopping = true
	for key, entry := range pool.entries {
		pool.removeLocked(key, entry)
	}
	pool.mu.Unlock()
}
