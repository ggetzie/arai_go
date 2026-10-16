package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// staticAssets serves the embedded static files with cache-busting. Templates
// link to them through the static template func, which appends ?v=<content
// hash>. Requests carrying the current hash are cached for a year, since a
// change to the file changes its URL. Unversioned requests (images referenced
// from CSS, the favicon, stale URLs) must revalidate, which is a cheap 304
// thanks to the ETag.
type staticAssets struct {
	fsys   fs.FS
	hashes map[string]string // path relative to static/ -> short content hash
}

const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "public, no-cache"
)

func newStaticAssets(fsys fs.FS) (*staticAssets, error) {
	a := &staticAssets{fsys: fsys, hashes: map[string]string{}}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a.hashes[path] = hex.EncodeToString(sum[:6])
		return nil
	})
	return a, err
}

// URL returns the versioned URL for a static file, e.g. "js/project.js" ->
// "/static/js/project.js?v=1a2b3c4d5e6f". An unknown name is an error, so a
// typo in a template fails at render time (and in tests).
func (a *staticAssets) URL(name string) (string, error) {
	hash, ok := a.hashes[name]
	if !ok {
		return "", fmt.Errorf("unknown static file %q", name)
	}
	return "/static/" + name + "?v=" + hash, nil
}

// ServeHTTP serves /static/<name>. Only embedded files are served, so there
// are no directory listings.
func (a *staticAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.serve(w, r, strings.TrimPrefix(r.URL.Path, "/static/"))
}

func (a *staticAssets) serve(w http.ResponseWriter, r *http.Request, name string) {
	hash, ok := a.hashes[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("ETag", `"`+hash+`"`)
	if r.URL.Query().Get("v") == hash {
		h.Set("Cache-Control", cacheImmutable)
	} else {
		h.Set("Cache-Control", cacheRevalidate)
	}
	// ServeFileFS answers If-None-Match with 304 using the ETag set above.
	http.ServeFileFS(w, r, a.fsys, name)
}
