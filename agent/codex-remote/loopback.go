package codexremote

import "sync"

type loopbackConn struct {
	out  chan Envelope
	in   chan Envelope
	done chan struct{}
	once *sync.Once
}

func (c *loopbackConn) Write(env Envelope) error {
	select {
	case <-c.done:
		return errClosedSentinel{}
	default:
	}
	select {
	case c.out <- env:
		return nil
	case <-c.done:
		return errClosedSentinel{}
	}
}

func (c *loopbackConn) Read() (Envelope, error) {
	select {
	case env, ok := <-c.in:
		if !ok {
			return Envelope{}, errClosedSentinel{}
		}
		return env, nil
	case <-c.done:
		return Envelope{}, errClosedSentinel{}
	}
}

func (c *loopbackConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

type errClosedSentinel struct{}

func (errClosedSentinel) Error() string { return "codex-remote: frame conn closed" }

// LoopbackPair returns two FrameConns that speak envelopes to each other.
func LoopbackPair() (FrameConn, FrameConn) {
	a2b := make(chan Envelope, 64)
	b2a := make(chan Envelope, 64)
	done := make(chan struct{})
	once := &sync.Once{}
	return &loopbackConn{out: a2b, in: b2a, done: done, once: once},
		&loopbackConn{out: b2a, in: a2b, done: done, once: once}
}
