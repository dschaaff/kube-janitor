package shutdown

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// GracefulShutdown handles graceful application shutdown
type GracefulShutdown struct {
	shutdownNow bool
	safeToExit  bool
	done        chan struct{}
	mu          sync.RWMutex
}

// NewGracefulShutdown creates a new GracefulShutdown handler
func NewGracefulShutdown() *GracefulShutdown {
	gs := &GracefulShutdown{
		done: make(chan struct{}),
	}

	go gs.handleSignals()
	return gs
}

// handleSignals handles OS signals for shutdown
func (gs *GracefulShutdown) handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	gs.mu.Lock()
	gs.shutdownNow = true
	gs.mu.Unlock()

	if gs.IsSafeToExit() {
		close(gs.done)
		os.Exit(0)
	}
}

// IsSafeToExit returns whether it's safe to exit
func (gs *GracefulShutdown) IsSafeToExit() bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.safeToExit
}

// SetSafeToExit sets whether it's safe to exit
func (gs *GracefulShutdown) SetSafeToExit(safe bool) {
	gs.mu.Lock()
	gs.safeToExit = safe
	if gs.shutdownNow && safe {
		close(gs.done)
		os.Exit(0)
	}
	gs.mu.Unlock()
}

// Done returns a channel that's closed when shutdown is complete
func (gs *GracefulShutdown) Done() <-chan struct{} {
	return gs.done
}

// NewContext creates a new context that will be canceled on shutdown signals
func NewContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	gs := NewGracefulShutdown()

	go func() {
		<-gs.Done()
		cancel()
	}()

	return ctx
}

// ShutdownWithContext creates a context that will be canceled on shutdown signals
// and returns the GracefulShutdown instance for additional control
func ShutdownWithContext() (context.Context, *GracefulShutdown) {
	ctx, cancel := context.WithCancel(context.Background())
	gs := NewGracefulShutdown()

	go func() {
		<-gs.Done()
		cancel()
	}()

	return ctx, gs
}
