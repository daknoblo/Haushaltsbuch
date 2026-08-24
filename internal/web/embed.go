package web

import (
	"embed"
	"io/fs"
)

// Assets holds the static assets (compiled CSS, JS) served under /static/ and
// embedded into the binary. The Tailwind source input.css stays out of the
// binary; only the compiled output is served.
//
//go:embed assets/static
var Assets embed.FS

// AssetsFS returns a file system rooted at the static directory, suitable for
// serving under the /static/ path.
func AssetsFS() fs.FS {
	sub, err := fs.Sub(Assets, "assets/static")
	if err != nil {
		panic(err)
	}
	return sub
}
