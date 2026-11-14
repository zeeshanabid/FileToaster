// Package filetoaster embeds the built SvelteKit frontend so the Go binary can
// serve the UI without any external files. Build the frontend first:
//
//  make build        # builds client/ and stages output into ./frontend-build
//  cd client && npm run build && cp -r frontend-build ./frontend-build
//
// which writes the static site to client/frontend-build; it is copied into
// ./frontend-build (the location referenced by the //go:embed directive below).
package filetoaster

import (
  "embed"
  "io/fs"
)

//go:embed all:frontend-build
var assetsFS embed.FS

// Assets exposes the embedded frontend as an fs.FS rooted at the build output.
var Assets fs.FS = mustSub(assetsFS, "frontend-build")

func mustSub(f embed.FS, name string) fs.FS {
  sub, err := fs.Sub(f, name)
  if err != nil {
    panic(err)
  }
  return sub
}
