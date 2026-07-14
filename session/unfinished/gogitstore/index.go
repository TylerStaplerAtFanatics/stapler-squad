package gogitstore

import (
	"sync"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
)

// lockedIndex wraps an idxfile.Index (concretely an *idxfile.MemoryIndex)
// and serialises every call behind mu.
//
// This is NOT a defensive-programming nicety — it is required for
// correctness. idxfile.MemoryIndex's "read" methods are not read-only:
// FindOffset and FindHash lazily populate an internal offsetHash map as a
// side effect of the lookup (see idxfile.go: `idx.offsetHash[int64(offset)]
// = h`). go-git issue #1121 documents the resulting
// "concurrent map read and map write" crash when a single MemoryIndex is
// used from more than one goroutine without external locking — this is
// exactly the situation this package deliberately creates on purpose (one
// MemoryIndex shared by every worktree of a repo), so every access MUST go
// through this wrapper. Never hand out the underlying idxfile.Index value
// directly.
//
// One SharedObjectStore uses a single mutex (its own mu) for every
// lockedIndex it hands out, rather than one mutex per pack. That trades
// away some parallelism (see design doc, "Concurrency trade-offs") for
// simplicity and a smaller blast radius while this is a prototype; sharding
// the lock per-pack is a documented follow-up, not a correctness issue.
type lockedIndex struct {
	mu  *sync.Mutex
	idx idxfile.Index
}

var _ idxfile.Index = (*lockedIndex)(nil)

func (l *lockedIndex) Contains(h plumbing.Hash) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.Contains(h)
}

func (l *lockedIndex) FindOffset(h plumbing.Hash) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.FindOffset(h)
}

func (l *lockedIndex) FindCRC32(h plumbing.Hash) (uint32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.FindCRC32(h)
}

func (l *lockedIndex) FindHash(o int64) (plumbing.Hash, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.FindHash(o)
}

func (l *lockedIndex) Count() (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.Count()
}

// Entries and EntriesByOffset return an iterator; the lock is held only
// while the iterator itself is constructed (idxfile's iterator Next()
// methods read immutable post-decode fields — Names/Fanout/etc — and do not
// touch offsetHash, so draining the returned iterator without the lock held
// is safe). Only FindOffset/FindHash mutate state after decode.
func (l *lockedIndex) Entries() (idxfile.EntryIter, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.Entries()
}

func (l *lockedIndex) EntriesByOffset() (idxfile.EntryIter, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idx.EntriesByOffset()
}
