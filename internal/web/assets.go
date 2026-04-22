package web

import "embed"

//go:embed static static/* static/assets/*
var Assets embed.FS
