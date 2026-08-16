package natswebgateway

import (
	"context"
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
	inflight map[[32]byte]*securityContextAttempt
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
	identity   credentials.Context
	generation uint64
}

type securityContextAttempt struct {
	done chan struct{}
	err  error
}

type securityContextLease struct {
	connection natsConnection
	tracker    *permissionTracker
	expiresAt  time.Time
	release    func()
}

func newSecurityContextPool(config SecurityContext, urls string, connect connectFunc, base []nats.Option) *securityContextPool {
	return &securityContextPool{
		entries: make(map[[32]byte]*securityContextEntry), inflight: make(map[[32]byte]*securityContextAttempt), config: config,
		urls: urls, connect: connect, base: base,
	}
}

func (pool *securityContextPool) acquire(ctx context.Context, adapted credentials.Context) (*securityContextLease, error) {
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		key, _, err := adapted.RefreshIdentity()
		if err != nil {
			return nil, err
		}
		pool.mu.Lock()
		if pool.stopping {
			pool.mu.Unlock()
			return nil, nats.ErrConnectionClosed
		}
		now := time.Now()
		pool.evictChangedLocked()
		if entry := pool.entries[key]; entry != nil {
			if entry.expired || now.Sub(entry.created) >= time.Duration(pool.config.MaxLifetime) {
				entry.expired = true
				if entry.references == 0 {
					pool.removeLocked(key, entry)
				} else {
					pool.mu.Unlock()
					return nil, nats.ErrConnectionReconnecting
				}
			} else if !entry.connection.IsConnected() {
				pool.mu.Unlock()
				return nil, nats.ErrConnectionReconnecting
			} else {
				entry.references++
				entry.lastUsed = now
				pool.scheduleLocked(key, entry)
				lease := pool.leaseLocked(key, entry)
				pool.mu.Unlock()
				return lease, nil
			}
		}
		attempt := pool.inflight[key]
		if attempt == nil {
			if len(pool.entries)+len(pool.inflight) >= pool.config.MaxConnections {
				pool.evictIdleLocked(now)
			}
			if len(pool.entries)+len(pool.inflight) >= pool.config.MaxConnections {
				pool.mu.Unlock()
				return nil, errSecurityContextLimit
			}
			attempt = &securityContextAttempt{done: make(chan struct{})}
			pool.inflight[key] = attempt
			go pool.connectContext(key, adapted, attempt, connectionTimeout(ctx))
		}
		pool.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-attempt.done:
			if attempt.err != nil {
				return nil, attempt.err
			}
		}
	}
}

func connectionTimeout(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return time.Nanosecond
	}
	return remaining
}

func (pool *securityContextPool) connectContext(key [32]byte, adapted credentials.Context, attempt *securityContextAttempt, timeout time.Duration) {
	tracker := newPermissionTracker()
	options := append([]nats.Option{}, pool.base...)
	options = append(options, nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { tracker.handle(err) }))
	options = append(options, adapted.Options...)
	if timeout > 0 {
		options = append(options, nats.Timeout(timeout))
	}
	connection, err := pool.connect(pool.urls, options...)

	pool.mu.Lock()
	defer pool.mu.Unlock()
	delete(pool.inflight, key)
	if err != nil {
		attempt.err = err
		close(attempt.done)
		return
	}
	observedKey, generation := adapted.ObservedIdentity()
	if pool.stopping {
		connection.Close()
		attempt.err = nats.ErrConnectionClosed
		close(attempt.done)
		return
	}
	if pool.entries[observedKey] != nil {
		connection.Close()
		close(attempt.done)
		return
	}
	now := time.Now()
	entry := &securityContextEntry{
		connection: connection, tracker: tracker, created: now, lastUsed: now,
		identity: adapted, generation: generation,
	}
	pool.entries[observedKey] = entry
	pool.scheduleLocked(observedKey, entry)
	close(attempt.done)
}

func (pool *securityContextPool) leaseLocked(key [32]byte, entry *securityContextEntry) *securityContextLease {
	var once sync.Once
	return &securityContextLease{
		connection: entry.connection,
		tracker:    entry.tracker,
		expiresAt:  entry.created.Add(time.Duration(pool.config.MaxLifetime)),
		release: func() {
			once.Do(func() { pool.release(key, entry) })
		},
	}
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

func (pool *securityContextPool) evictChangedLocked() {
	for key, entry := range pool.entries {
		if entry.identity.IdentityChanged(entry.generation) {
			entry.expired = true
			if entry.references == 0 {
				pool.removeLocked(key, entry)
			}
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
