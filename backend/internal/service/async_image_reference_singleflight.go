package service

import "context"

// Serialize cache fills for one exact URL without tying waiting callers to
// the first caller's cancellation. Every caller still applies its own budget.
func (c *AsyncImageReferenceCache) acquireURL(ctx context.Context, key string) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	for {
		c.mu.Lock()
		if c.inflight == nil {
			c.inflight = make(map[string]chan struct{})
		}
		if done, ok := c.inflight[key]; ok {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		c.inflight[key] = done
		c.mu.Unlock()
		return func() { c.mu.Lock(); delete(c.inflight, key); close(done); c.mu.Unlock() }, nil
	}
}
