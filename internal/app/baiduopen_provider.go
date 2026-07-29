package app

import (
	"strings"

	"NyaMedia/internal/model"
	baiduopenprovider "NyaMedia/internal/provider/baiduopen"
)

func (a *App) getOrCreateBaiduOpenProvider(
	providerModel model.Provider,
	clientID,
	clientSecret,
	accessToken,
	refreshToken,
	expiresAt string,
) *baiduopenprovider.Provider {
	a.baiduProviderMu.Lock()
	defer a.baiduProviderMu.Unlock()

	cached, cachedExists := a.baiduProviders[providerModel.ID]
	if cachedExists &&
		cached.rootPath == providerModel.RootPath &&
		cached.clientID == clientID &&
		cached.clientSecret == clientSecret {
		return cached.provider
	}

	tokenState := baiduopenprovider.NewTokenState(accessToken, refreshToken, expiresAt)
	if cachedExists && cached.clientID == clientID && cached.clientSecret == clientSecret {
		tokenState = cached.tokenState
	}
	runtimeProvider := baiduopenprovider.NewWithTokenState(
		providerModel.ID,
		providerModel.RootPath,
		clientID,
		clientSecret,
		tokenState,
		func(updatedAccessToken, updatedRefreshToken, updatedExpiresAt string) {
			a.persistProviderBaiduOpenTokens(
				providerModel.ID,
				clientID,
				clientSecret,
				updatedAccessToken,
				updatedRefreshToken,
				updatedExpiresAt,
			)
		},
		providerCacheScope{app: a, providerID: providerModel.ID},
	)
	if a.baiduProviders == nil {
		a.baiduProviders = make(map[string]cachedBaiduOpenProvider)
	}
	a.baiduProviders[providerModel.ID] = cachedBaiduOpenProvider{
		rootPath:     providerModel.RootPath,
		clientID:     strings.TrimSpace(clientID),
		clientSecret: strings.TrimSpace(clientSecret),
		tokenState:   tokenState,
		provider:     runtimeProvider,
	}
	return runtimeProvider
}

func (a *App) invalidateBaiduOpenProvider(providerID string) {
	a.baiduProviderMu.Lock()
	delete(a.baiduProviders, providerID)
	a.baiduProviderMu.Unlock()
}
