package gogitstore

import (
	"sync"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
)

// Registry hands out one *SharedObjectStore per commondir, so every
// worktree of the same repository resolves to the same shared index +
// object cache. Callers should construct one Registry per process (or per
// logical "pool of repos this component cares about") and reuse it across
// every Open call — a fresh Registry per call defeats the entire point.
//
// Prototype scope: this Registry never evicts entries. A production
// version needs the same kind of TTL/LRU/memory-budget eviction
// session/unfinished/cache.go's GoGitVCSReader.repoCache already has
// (pruneRepoCache/effectiveCacheBudgetBytes) — except keyed by commondir
// and reference-counted by live WorktreeStorers, since a SharedObjectStore
// must not be evicted while any worktree is still using it. See the design
// doc's rollout plan, stage 2.
type Registry struct {
	// CacheMaxSize is the per-commondir decoded-object LRU cache size in
	// bytes, passed to cache.NewObjectLRU for each new SharedObjectStore.
	// Zero-value Registry uses cache.DefaultMaxSize (go-git's own 96MB
	// default) — callers migrating off the OOM-prone default should set
	// this explicitly (e.g. 12MB, matching the parallel cache-sharing fix
	// in gogit_vcs_reader.go).
	CacheMaxSize int64

	// LargeObjectThreshold is forwarded to packfile.NewPackfileWithCache;
	// see storage/filesystem.Options.LargeObjectThreshold upstream. Zero
	// means "no limit" (matches upstream's zero-value behavior).
	LargeObjectThreshold int64

	mu     sync.Mutex
	stores map[string]*SharedObjectStore // key: filepath.Clean'd absolute commondir
}

// NewRegistry returns a Registry using go-git's own default object-cache
// size (96MB) per commondir. Use &Registry{CacheMaxSize: N} directly to
// override it.
func NewRegistry() *Registry {
	return &Registry{}
}

// acquire returns the SharedObjectStore for commonDirAbs, creating it on
// first use. commonDirAbs must already be an absolute, filepath.Clean'd
// path — callers (open.go) are responsible for normalizing it so that two
// different-looking-but-equal paths (e.g. a trailing slash) don't
// accidentally create two stores for one commondir.
func (r *Registry) acquire(commonDirAbs string, commonFs billy.Filesystem) *SharedObjectStore {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stores == nil {
		r.stores = make(map[string]*SharedObjectStore)
	}
	if s, ok := r.stores[commonDirAbs]; ok {
		return s
	}

	maxSize := r.CacheMaxSize
	if maxSize <= 0 {
		maxSize = int64(cache.DefaultMaxSize)
	}
	s := newSharedObjectStore(commonDirAbs, commonFs, cache.NewObjectLRU(cache.FileSize(maxSize)), r.LargeObjectThreshold)
	r.stores[commonDirAbs] = s
	return s
}

// Stats returns a snapshot of (commondir -> parsed index-entry count) for
// every SharedObjectStore this Registry has ever created — prototype
// introspection for tests/manual verification, not meant as a stable API.
func (r *Registry) Stats() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.stores))
	for k, s := range r.stores {
		out[k] = s.IndexEntryCount
	}
	return out
}
