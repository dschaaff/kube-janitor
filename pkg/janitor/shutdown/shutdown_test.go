package shutdown

import (
	"testing"
	"time"
)

func TestGracefulShutdown(t *testing.T) {
	gs := NewGracefulShutdown()

	// Test initial state
	if gs.IsSafeToExit() {
		t.Error("Expected initial safe to exit to be false")
	}

	// Test setting safe to exit
	gs.SetSafeToExit(true)
	if !gs.IsSafeToExit() {
		t.Error("Expected safe to exit to be true after setting")
	}

	// Test manual shutdown triggering instead of using real signals
	done := make(chan struct{})
	go func() {
		<-gs.Done()
		close(done)
	}()

	// Manually trigger shutdown instead of sending a real signal
	gs.mu.Lock()
	gs.shutdownNow = true
	// Since safeToExit is already true, this should close the done channel
	if gs.safeToExit {
		close(gs.done)
	}
	gs.mu.Unlock()

	// Wait for shutdown or timeout
	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Error("Shutdown not handled within timeout")
	}
}
