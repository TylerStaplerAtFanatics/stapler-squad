// Package gogitstore is a prototype of a pluggable go-git storage.Storer
// that shares the two expensive, commondir-scoped pieces of repository
// state — the parsed packfile index and the decoded-object cache — across
// every worktree of one repository, instead of paying for them once per
// opened worktree the way git.PlainOpenWithOptions does.
//
// See session/unfinished/design/pluggable-gitstore.md for the full design
// rationale, the concurrency-safety analysis this package is built around,
// and the staged rollout plan. This package is a read-only prototype: it
// implements enough of storer.EncodedObjectStorer to satisfy git.Open() and
// to serve the three read operations session/unfinished's Scanner actually
// needs (HasUncommitted, DiffShortstat, AheadBehind — see
// session/unfinished/vcsreader.go). Write-path methods
// (SetEncodedObject/PackfileWriter/etc.) are intentionally not implemented.
package gogitstore

import (
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
	"github.com/go-git/go-git/v5/plumbing/format/objfile"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

// errNotImplemented is returned by the write-path EncodedObjectStorer
// methods this prototype deliberately does not implement (see package doc).
var errNotImplemented = errors.New("gogitstore: not implemented in this read-only prototype")

// SharedObjectStore holds everything that is scoped to a repository's
// commondir (the shared git object database root: $GIT_COMMON_DIR/objects)
// rather than to any single worktree: the parsed packfile indexes and the
// decoded-object LRU cache. Exactly one SharedObjectStore should exist per
// commondir for the lifetime of the process — obtain one via Registry,
// never construct directly from outside this package.
//
// Every worktree of one repository shares the SAME on-disk object
// database (that's what "commondir" means in git's own worktree design —
// see storage/filesystem/dotgit/repository_filesystem.go upstream, which
// this package's split mirrors at the Storer layer instead of the raw
// filesystem layer). There is therefore no correctness reason for each
// worktree to parse its own copy of every .idx file or keep its own
// decoded-object cache — this type exists purely to stop paying that cost
// N times.
type SharedObjectStore struct {
	commonDirAbs string
	dir          *dotgit.DotGit // rooted at the commondir filesystem
	fs           billy.Filesystem
	objectCache  cache.Object // shared decoded-object LRU; already internally mutex-protected (plumbing/cache/object_lru.go)

	largeObjectThreshold int64

	// mu guards TWO independently-discovered pieces of unsynchronized
	// upstream state that this store deliberately shares across worktrees:
	//   1. index/indexBuilt, and every lockedIndex (index.go) handed out by
	//      this store — idxfile.MemoryIndex mutates internal state
	//      (offsetHash) even on lookups (go-git issue #1121).
	//   2. every call into s.dir (dirObject/dirObjectPack below) —
	//      *dotgit.DotGit has its own unguarded lazily-populated caches
	//      (incomingChecked/objectList/packList fields in
	//      storage/filesystem/dotgit/dotgit.go's DotGit struct) that are NOT
	//      safe to touch from more than one goroutine either. This was found
	//      empirically, not anticipated up front — see the design doc §4.3.
	// See index.go's package doc for why a single mutex per store (not per
	// pack) is used for (1); the same reasoning applies to (2).
	mu         sync.Mutex
	index      map[plumbing.Hash]*lockedIndex
	indexBuilt bool

	// IndexBuildCount and IndexEntryCount are prototype instrumentation
	// (exported so the package's own test — and any caller who wants to
	// verify the sharing actually happened — can assert on them without
	// reaching into unexported state). IndexBuildCount should be exactly 1
	// no matter how many worktrees share this store; a second parse would
	// mean the sharing is broken.
	IndexBuildCount int32
	IndexEntryCount int64
}

func newSharedObjectStore(commonDirAbs string, commonFs billy.Filesystem, objectCache cache.Object, largeObjectThreshold int64) *SharedObjectStore {
	return &SharedObjectStore{
		commonDirAbs:         commonDirAbs,
		dir:                  dotgit.New(commonFs),
		fs:                   commonFs,
		objectCache:          objectCache,
		largeObjectThreshold: largeObjectThreshold,
		index:                make(map[plumbing.Hash]*lockedIndex),
	}
}

// ensureIndex parses every packfile .idx once. Concurrent callers racing on
// the first call all block on mu; only the first actually parses anything.
func (s *SharedObjectStore) ensureIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexBuilt {
		return nil
	}

	packs, err := s.dir.ObjectPacks()
	if err != nil {
		return err
	}

	built := make(map[plumbing.Hash]*lockedIndex, len(packs))
	var totalEntries int64
	for _, h := range packs {
		f, err := s.dir.ObjectPackIdx(h)
		if err != nil {
			return err
		}
		mi := idxfile.NewMemoryIndex()
		derr := idxfile.NewDecoder(f).Decode(mi)
		_ = f.Close()
		if derr != nil {
			return derr
		}
		built[h] = &lockedIndex{mu: &s.mu, idx: mi}
		if n, cerr := mi.Count(); cerr == nil {
			totalEntries += n
		}
	}

	s.index = built
	s.indexBuilt = true
	atomic.AddInt32(&s.IndexBuildCount, 1)
	atomic.AddInt64(&s.IndexEntryCount, totalEntries)
	return nil
}

// dirObject and dirObjectPack are locked wrappers around s.dir.Object and
// s.dir.ObjectPack. This is NOT the same lock-scope story as lockedIndex —
// see the package/type doc above: dotgit.DotGit itself (storage/filesystem/
// dotgit/dotgit.go) has multiple unguarded, lazily-populated caches
// (incomingChecked/incomingDirName, objectList/objectMap, packList/packMap)
// that Object/ObjectPack/ObjectPacks/hasObject/hasPack read and populate
// with no locking of their own — sharing one *dotgit.DotGit across worktrees
// (which this store deliberately does, for the same reason it shares the
// parsed index) means every call into it needs external synchronization
// too, not just calls into the parsed idxfile.Index. The lock is held only
// around the call that resolves a path and opens the billy.File — once a
// handle is returned, reading FROM it (decompression, packfile Scanner
// work) never touches d's shared state again, so the lock does not extend
// over the expensive part of the read.
func (s *SharedObjectStore) dirObject(h plumbing.Hash) (billy.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir.Object(h)
}

func (s *SharedObjectStore) dirObjectPack(pack plumbing.Hash) (billy.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir.ObjectPack(pack)
}

// findObjectInPackfile returns which pack (if any) contains h and its
// offset within that pack. It snapshots the index map under mu, then
// releases mu before calling FindOffset on each entry (each of those calls
// re-acquires mu itself via lockedIndex) — never held twice at once, so
// this cannot deadlock against lockedIndex's own locking.
func (s *SharedObjectStore) findObjectInPackfile(h plumbing.Hash) (pack plumbing.Hash, offset int64, ok bool) {
	s.mu.Lock()
	packs := make(map[plumbing.Hash]*lockedIndex, len(s.index))
	for k, v := range s.index {
		packs[k] = v
	}
	s.mu.Unlock()

	for packHash, idx := range packs {
		off, err := idx.FindOffset(h)
		if err == nil {
			return packHash, off, true
		}
	}
	return plumbing.ZeroHash, 0, false
}

// EncodedObject implements the shared, read-only half of
// storer.EncodedObjectStorer. WorktreeStorer.EncodedObject delegates here
// directly — there is no per-worktree object storage at all in this
// prototype, only per-worktree Reference/Index/Shallow/Config storage (see
// storer.go).
func (s *SharedObjectStore) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	obj, err := s.getFromUnpacked(h)
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		obj, err = s.getFromPackfile(h)
	}
	if err != nil {
		return nil, err
	}
	if t != plumbing.AnyObject && obj.Type() != t {
		return nil, plumbing.ErrObjectNotFound
	}
	return obj, nil
}

func (s *SharedObjectStore) getFromUnpacked(h plumbing.Hash) (obj plumbing.EncodedObject, err error) {
	f, err := s.dirObject(h)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, plumbing.ErrObjectNotFound
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if cached, found := s.objectCache.Get(h); found {
		return cached, nil
	}

	r, err := objfile.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	typ, size, err := r.Header()
	if err != nil {
		return nil, err
	}

	mo := &plumbing.MemoryObject{}
	mo.SetType(typ)
	mo.SetSize(size)
	w, err := mo.Writer()
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(w, r); err != nil {
		return nil, err
	}
	s.objectCache.Put(mo)
	return mo, nil
}

// getFromPackfile opens a FRESH billy.File handle to the pack for every
// call rather than caching one per store. This is a deliberate prototype
// simplification, not an accident: a *packfile.Packfile / *packfile.Scanner
// pair holds a seek cursor over its billy.File, so sharing ONE open pack
// handle across concurrent worktree reads would race exactly like sharing
// the raw MemoryIndex does. Opening a fresh os-level file handle per call
// sidesteps that: concurrent reads of the same pack get independent file
// descriptors (cheap; the OS page cache is already shared underneath them),
// while the expensive parts — the parsed idxfile.Index and the decoded
// cache.Object — are still the single shared instances from this store. A
// production version should cache one *packfile.Packfile per (worktree,
// pack) instead, mirroring filesystem.ObjectStorage's
// KeepDescriptors/MaxOpenDescriptors knobs — see the design doc's rollout
// plan.
func (s *SharedObjectStore) getFromPackfile(h plumbing.Hash) (plumbing.EncodedObject, error) {
	if err := s.ensureIndex(); err != nil {
		return nil, err
	}
	pack, offset, ok := s.findObjectInPackfile(h)
	if !ok {
		return nil, plumbing.ErrObjectNotFound
	}

	s.mu.Lock()
	idx := s.index[pack]
	s.mu.Unlock()

	f, err := s.dirObjectPack(pack)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	p := packfile.NewPackfileWithCache(idx, s.fs, f, s.objectCache, s.largeObjectThreshold)
	defer func() { _ = p.Close() }()

	return p.GetByOffset(offset)
}

// HasEncodedObject implements storer.EncodedObjectStorer.
func (s *SharedObjectStore) HasEncodedObject(h plumbing.Hash) error {
	if f, err := s.dirObject(h); err == nil {
		_ = f.Close()
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := s.ensureIndex(); err != nil {
		return err
	}
	if _, _, ok := s.findObjectInPackfile(h); !ok {
		return plumbing.ErrObjectNotFound
	}
	return nil
}

// EncodedObjectSize implements storer.EncodedObjectStorer.
//
// This is a prototype simplification: it fully materializes the object via
// EncodedObject rather than reading just the size from the object/pack
// header the way filesystem.ObjectStorage.EncodedObjectSize does. None of
// the three scanner operations this package targets (HasUncommitted,
// DiffShortstat, AheadBehind) call EncodedObjectSize, so this is not on the
// hot path — flagged here rather than optimized, per the task's read-only
// read-path-only scope.
func (s *SharedObjectStore) EncodedObjectSize(h plumbing.Hash) (int64, error) {
	obj, err := s.EncodedObject(plumbing.AnyObject, h)
	if err != nil {
		return 0, err
	}
	return obj.Size(), nil
}

// IterEncodedObjects is intentionally unimplemented. None of
// HasUncommitted/DiffShortstat/AheadBehind (session/unfinished/vcsreader.go)
// walk the full object set — they all resolve specific hashes (HEAD,
// blobs, commits reachable via parent links) via EncodedObject. A full
// materialization/GC/fsck-style caller would need this implemented
// (mirroring filesystem.ObjectStorage.IterEncodedObjects — loose-object
// directory scan + per-pack iterators); left as a documented follow-up.
func (s *SharedObjectStore) IterEncodedObjects(plumbing.ObjectType) (storer.EncodedObjectIter, error) {
	return nil, errNotImplemented
}
