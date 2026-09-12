package gobridge

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Var only so tests can shrink the bounded real scan timeout.
var backgroundTaskScanTimeout = 30 * time.Second

type backgroundTaskFlight struct {
	done     chan struct{}
	tasks    []core.BackgroundTask
	err      error
	complete bool
}

type backgroundTaskCache struct {
	mu      sync.Mutex
	flights map[string]*backgroundTaskFlight
}

func newBackgroundTaskCache() *backgroundTaskCache {
	return &backgroundTaskCache{flights: make(map[string]*backgroundTaskFlight)}
}

func backgroundTaskFlightKey(backendID, sessionID string, syncRev int) string {
	return backendID + "|" + sessionID + "|" + strconv.Itoa(syncRev)
}

// fetch single-flights one full scan for a projection revision. Successful results
// are reusable at the same revision; errors are observed by followers but retried
// by the next request. scan runs on the caller's root context and is bounded by
// scan itself, so bridge shutdown cancels in-flight work.
func (c *backgroundTaskCache) fetch(
	ctx context.Context,
	backendID, sessionID string,
	syncRev int,
	scan func(context.Context) ([]core.BackgroundTask, error),
) ([]core.BackgroundTask, error) {
	key := backgroundTaskFlightKey(backendID, sessionID, syncRev)
	flight, leader := c.start(key)
	if !leader {
		select {
		case <-flight.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return flight.tasks, flight.err
	}
	scanCtx, cancel := context.WithTimeout(ctx, backgroundTaskScanTimeout)
	tasks, err := scan(scanCtx)
	cancel()
	c.finish(key, tasks, err)
	return tasks, err
}

func (c *backgroundTaskCache) start(key string) (*backgroundTaskFlight, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if flight := c.flights[key]; flight != nil {
		if !flight.complete || flight.err == nil {
			return flight, false
		}
		// Completed errors are not negative-cached. Replace with a real retry.
	}
	flight := &backgroundTaskFlight{done: make(chan struct{})}
	c.flights[key] = flight
	return flight, true
}

func (c *backgroundTaskCache) finish(key string, tasks []core.BackgroundTask, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	flight := c.flights[key]
	if flight == nil || flight.complete {
		return
	}
	flight.tasks = append([]core.BackgroundTask(nil), tasks...)
	flight.err = err
	flight.complete = true
	close(flight.done)
	if err != nil {
		delete(c.flights, key)
	}
}

func (c *backgroundTaskCache) lookup(backendID, sessionID string, syncRev int) ([]core.BackgroundTask, bool) {
	key := backgroundTaskFlightKey(backendID, sessionID, syncRev)
	c.mu.Lock()
	defer c.mu.Unlock()
	flight := c.flights[key]
	if flight == nil || !flight.complete || flight.err != nil {
		return nil, false
	}
	return append([]core.BackgroundTask(nil), flight.tasks...), true
}

// pruneCompletedSession keeps the current revision's completed flight and removes
// older completed revisions. In-flight older flights remain until they close.
func (c *backgroundTaskCache) pruneCompletedSession(backendID, sessionID string, keepRev int) {
	prefix := backendID + "|" + sessionID + "|"
	keep := backgroundTaskFlightKey(backendID, sessionID, keepRev)
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, flight := range c.flights {
		if key == keep || !strings.HasPrefix(key, prefix) || !flight.complete {
			continue
		}
		delete(c.flights, key)
	}
}
