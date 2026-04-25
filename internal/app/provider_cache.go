package app

import (
	"context"
	"log"
	"time"
)

type providerCacheScope struct {
	app        *App
	providerID string
}

func (s providerCacheScope) Get(ctx context.Context, key string) (string, bool, error) {
	return s.app.providerCache.Get(ctx, s.providerID, key)
}

func (s providerCacheScope) Set(ctx context.Context, key, value string) error {
	return s.app.providerCache.Set(ctx, s.providerID, key, value)
}

func (s providerCacheScope) SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.app.providerCache.SetWithTTL(ctx, s.providerID, key, value, ttl)
}

func (a *App) startProviderCachePruner(ctx context.Context) {
	if _, err := a.providerCache.DeleteExpired(ctx); err != nil {
		log.Printf("delete expired provider cache: %v", err)
	}

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := a.providerCache.DeleteExpired(ctx); err != nil {
				log.Printf("delete expired provider cache: %v", err)
			}
		}
	}
}
