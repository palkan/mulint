package tests

import "sync"

// Shapes distilled from tailscale wgengine/magicsock: a method call on a
// nested field whose type has its own mutex with the same field name ("mu")
// and the same receiver name ("c") must not be confused with the caller's
// mutex.

type instConn struct {
	mu    sync.Mutex
	pconn *instPacketConn
}

type instPacketConn struct {
	mu     sync.Mutex
	closed bool
}

func (c *instPacketConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func (c *instConn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// instPacketConn.Close locks its own "c.mu", not ours: no diagnostic.
	c.pconn.Close()
}

func (c *instConn) reopen() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Close() // want "Mutex lock is acquired on this line"
}

// Nested-owner regression guard: a method on the *same* receiver that locks a
// mutex owned by a nested field must still be detected.

type instHolder struct {
	in instPacketConn
}

func (h *instHolder) work() {
	h.in.mu.Lock()
	defer h.in.mu.Unlock()

	h.lockNested() // want "Mutex lock is acquired on this line"
}

func (h *instHolder) lockNested() {
	h.in.mu.Lock()
	h.in.closed = true
	h.in.mu.Unlock()
}
