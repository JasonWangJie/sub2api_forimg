package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAsyncImageReferenceCacheSerializesSameURLAndPreservesCancellation(t *testing.T) {
	c := NewAsyncImageReferenceCache(time.Minute, 1024)
	release, err := c.acquireURL(context.Background(), "same")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.acquireURL(ctx, "same")
	require.ErrorIs(t, err, context.Canceled)
	other, err := c.acquireURL(context.Background(), "other")
	require.NoError(t, err)
	other()
	done := make(chan struct{})
	go func() {
		unlock, e := c.acquireURL(context.Background(), "same")
		if e == nil {
			unlock()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("same URL must wait for first fill")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cache waiter did not resume")
	}
}
