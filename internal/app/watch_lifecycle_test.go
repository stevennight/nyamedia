package app

import (
	"context"
	"testing"
	"time"
)

func TestStopWatchTimersWaitsForDebouncedRescans(t *testing.T) {
	a := &App{watchTimers: make(map[string]*providerWatchTimer)}
	a.scheduleLibraryRescan(context.Background(), "library-a", nil)
	a.scheduleLibraryRescan(context.Background(), "library-a", nil)

	done := make(chan struct{})
	go func() {
		a.stopWatchTimers()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopWatchTimers did not release stopped debounce callbacks")
	}
	if len(a.watchTimers) != 0 {
		t.Fatalf("watch timers remaining = %d; want 0", len(a.watchTimers))
	}
}

func TestProviderWatcherReloadRequestsAreCoalesced(t *testing.T) {
	a := &App{watchReload: make(chan struct{}, 1)}
	a.requestProviderWatcherReload()
	a.requestProviderWatcherReload()
	if len(a.watchReload) != 1 {
		t.Fatalf("queued watcher reloads = %d; want 1", len(a.watchReload))
	}
}
