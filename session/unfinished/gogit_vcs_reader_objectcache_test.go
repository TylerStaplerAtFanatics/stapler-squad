package unfinished

// White-box tests for the object-cache quick fix: shrinking go-git's
// per-repo decoded-object cache from its 96MB default and sharing one
// instance across every worktree of the same repo (keyed by common .git
// dir, not worktree path). See openRepoEntry/sharedObjectCache in
// gogit_vcs_reader.go and session/unfinished/design/pluggable-gitstore.md
// for the larger, not-yet-wired-in follow-up (sharing the pack-index parse
// itself, which this fix does not attempt).

import (
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/cache"
)

// TestPerRepoObjectCacheSize_IsSmallerThanGoGitDefault guards the actual
// point of this fix: go-git's own default (cache.DefaultMaxSize, 96MB) is
// far larger than what a scanner holding many repos "hot" concurrently
// should pay per repo. This test fails if a future edit silently reverts to
// (or exceeds) go-git's default.
func TestPerRepoObjectCacheSize_IsSmallerThanGoGitDefault(t *testing.T) {
	if perRepoObjectCacheSize >= cache.DefaultMaxSize {
		t.Errorf("perRepoObjectCacheSize = %d, want strictly less than go-git's own default %d",
			perRepoObjectCacheSize, cache.DefaultMaxSize)
	}
}

// TestSharedObjectCache_SamePointerForSameCommonDir proves the cache is
// actually reused, not just re-created with the same size — the point of
// LoadOrStore over a plain Store.
func TestSharedObjectCache_SamePointerForSameCommonDir(t *testing.T) {
	g := &GoGitVCSReader{}
	commonDir := t.TempDir()

	first := g.sharedObjectCache(commonDir)
	second := g.sharedObjectCache(commonDir)

	if first != second {
		t.Errorf("sharedObjectCache(%q) returned different *cache.ObjectLRU pointers on two calls with the same commonDir; want the same instance reused", commonDir)
	}
	if first.MaxSize != perRepoObjectCacheSize {
		t.Errorf("shared cache MaxSize = %d, want %d (perRepoObjectCacheSize)", first.MaxSize, perRepoObjectCacheSize)
	}
}

// TestSharedObjectCache_DifferentPointerForDifferentCommonDir guards against
// a key-collision bug that would silently share unrelated repos' caches.
func TestSharedObjectCache_DifferentPointerForDifferentCommonDir(t *testing.T) {
	g := &GoGitVCSReader{}
	a := g.sharedObjectCache(t.TempDir())
	b := g.sharedObjectCache(t.TempDir())

	if a == b {
		t.Error("sharedObjectCache returned the same pointer for two distinct commonDir values; want distinct caches per repo")
	}
}

// TestOpenRepoEntry_TwoWorktreesOfSameRepo_ShareOneObjectCache is the
// end-to-end proof: two real linked worktrees of one repo, opened through
// openRepoEntry exactly as the scanner does, must end up backed by the same
// underlying *cache.ObjectLRU instance keyed by their shared common .git
// dir — the actual "quick fix" consolidation this change exists for.
func TestOpenRepoEntry_TwoWorktreesOfSameRepo_ShareOneObjectCache(t *testing.T) {
	mainRepo := initRepoInternal(t)
	linkedPath := filepath.Join(filepath.Dir(mainRepo), "linked-worktree-objcache")
	gitRunInternal(t, mainRepo, "worktree", "add", linkedPath, "-b", "objcache-feature")

	g := &GoGitVCSReader{}

	if _, err := g.openRepoEntry(mainRepo); err != nil {
		t.Fatalf("openRepoEntry(mainRepo): %v", err)
	}
	if _, err := g.openRepoEntry(linkedPath); err != nil {
		t.Fatalf("openRepoEntry(linkedPath): %v", err)
	}

	commonDir := gitCommonDir(mainRepo)
	var count int
	g.objectCacheByCommonDir.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("objectCacheByCommonDir has %d entries after opening 2 worktrees of 1 repo, want exactly 1 (shared by commondir %q)", count, commonDir)
	}
}
