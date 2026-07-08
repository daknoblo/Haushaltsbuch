package web

import (
	"embed"
	"io/fs"
)

// Assets holds the static assets (CSS, JS) served under /assets/ and embedded
// into the binary.
//
//go:embed assets/*
var Assets embed.FS

// AssetsFS returns a file system rooted at the assets directory, suitable for
// serving under the /assets/ path.
func AssetsFS() fs.FS {
	sub, err := fs.Sub(Assets, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}
