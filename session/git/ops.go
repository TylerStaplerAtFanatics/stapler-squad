package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/tstapler/stapler-squad/executor/safeexec"
	"github.com/tstapler/stapler-squad/log"
)

// FetchBranch fetches a specific branch from the origin remote.
func FetchBranch(repoPath, branchName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := safeexec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "origin", branchName)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("failed to fetch branch: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("failed to fetch branch: %w", err)
	}
	return nil
}

// IsCommitOnMain reports whether sha has actually landed on mainBranch — either the
// local branch (a commit merged directly to main without ever going through a PR) or
// origin's copy (a PR merged remotely on GitHub that hasn't been pulled locally yet).
// Approval (a passing review verdict) and shipping are different questions; this
// answers only the second one, and does so by checking ancestry rather than trusting
// any cached "PR merged" flag, since that flag can be stale, absent (no PR was ever
// opened), or simply wrong for a manually-merged branch.
//
// Uses go-git rather than shelling out (repo convention — see
// .claude/rules/prefer-go-git-over-subshells.md). The origin fetch is best-effort: a
// failure (offline, no such remote, nothing new) does not fail the whole check, since
// the local-main check alone still answers the "merged directly to main locally" case.
func IsCommitOnMain(repoPath, mainBranch, sha string) (bool, error) {
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return false, fmt.Errorf("failed to open git repo at %s: %w", repoPath, err)
	}

	refSpec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", mainBranch, mainBranch))
	if fetchErr := repo.Fetch(&git.FetchOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{refSpec}}); fetchErr != nil && !errors.Is(fetchErr, git.NoErrAlreadyUpToDate) {
		log.Warn("IsCommitOnMain: fetch origin failed, falling back to local main only", "repoPath", repoPath, "err", fetchErr)
	}

	shaCommit, err := repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		return false, fmt.Errorf("failed to resolve commit %s in %s: %w", sha, repoPath, err)
	}

	onLocal, localErr := isAncestorOfRef(repo, shaCommit, plumbing.NewBranchReferenceName(mainBranch))
	if localErr != nil {
		log.Warn("IsCommitOnMain: local main ref check failed, falling back to origin/main", "repoPath", repoPath, "err", localErr)
	} else if onLocal {
		return true, nil
	}

	return isAncestorOfRef(repo, shaCommit, plumbing.NewRemoteReferenceName("origin", mainBranch))
}

// isAncestorOfRef resolves ref to its commit and reports whether commit is an
// ancestor of it (i.e. commit is already contained in ref's history).
func isAncestorOfRef(repo *git.Repository, commit *object.Commit, ref plumbing.ReferenceName) (bool, error) {
	r, err := repo.Reference(ref, true)
	if err != nil {
		return false, fmt.Errorf("failed to resolve ref %s: %w", ref, err)
	}
	target, err := repo.CommitObject(r.Hash())
	if err != nil {
		return false, fmt.Errorf("failed to resolve commit for ref %s: %w", ref, err)
	}
	return commit.IsAncestor(target)
}

// CheckoutBranch checks out a branch in an existing repository.
func CheckoutBranch(repoPath, branchName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := safeexec.CommandContext(ctx, "git", "-C", repoPath, "checkout", branchName)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("failed to checkout branch: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("failed to checkout branch: %w", err)
	}
	return nil
}

// RemoteURL returns the URL of the named remote (usually "origin") for a local repo.
func RemoteURL(repoPath, remote string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := safeexec.CommandContext(ctx, "git", "-C", repoPath, "remote", "get-url", remote)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("failed to get remote URL: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeMainResult describes the outcome of MergeMainIntoWorktree.
type MergeMainResult struct {
	// UpToDate is true when the worktree's branch already contained everything
	// from mainBranch — nothing was merged in.
	UpToDate bool
	// Merged is true when the merge (including a fast-forward) brought in new
	// commits from mainBranch.
	Merged bool
	// Conflicted is true when merging mainBranch produced conflicts. The merge is
	// always aborted before returning, so the worktree is left clean either way —
	// callers never have to clean up a half-merged tree.
	Conflicted bool
	// ConflictedFiles lists the paths that conflicted. Populated only when
	// Conflicted is true.
	ConflictedFiles []string
}

// MergeMainIntoWorktree fetches mainBranch from origin and merges it into whatever
// branch is currently checked out in worktreePath. It never leaves the worktree in a
// conflicted state: on conflict it aborts the merge immediately (via `git merge
// --abort`) and reports the conflicting paths, so the caller can hand that context to
// whoever resolves it rather than leaving a half-merged working tree behind for the
// next thing that touches it.
func MergeMainIntoWorktree(worktreePath, mainBranch string) (*MergeMainResult, error) {
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer fetchCancel()
	fetchCmd := safeexec.CommandContext(fetchCtx, "git", "-C", worktreePath, "fetch", "origin", mainBranch)
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %s (%w)", mainBranch, out, err)
	}

	// Capture HEAD before the merge so up-to-date can be detected by comparing SHAs
	// rather than parsing merge output text ("Already up to date." is locale- and
	// git-version-dependent, e.g. older git prints "Already up-to-date.").
	beforeSHA, headErr := getHeadCommitSHA(worktreePath)
	if headErr != nil {
		return nil, fmt.Errorf("failed to resolve HEAD before merge: %w", headErr)
	}

	mergeCtx, mergeCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer mergeCancel()
	mergeCmd := safeexec.CommandContext(mergeCtx, "git", "-C", worktreePath, "merge", "--no-edit", "origin/"+mainBranch)
	mergeOut, mergeErr := mergeCmd.CombinedOutput()
	if mergeErr == nil {
		afterSHA, headErr := getHeadCommitSHA(worktreePath)
		if headErr != nil {
			return nil, fmt.Errorf("failed to resolve HEAD after merge: %w", headErr)
		}
		if afterSHA == beforeSHA {
			return &MergeMainResult{UpToDate: true}, nil
		}
		return &MergeMainResult{Merged: true}, nil
	}

	// The merge failed. Distinguish real conflicts (recoverable — abort and report)
	// from any other git failure (propagate as-is; aborting a non-conflict failure
	// could mask the real problem).
	conflictFiles, conflictErr := conflictedFiles(worktreePath)
	if conflictErr != nil || len(conflictFiles) == 0 {
		return nil, fmt.Errorf("failed to merge %s: %s (%w)", mainBranch, mergeOut, mergeErr)
	}

	abortCtx, abortCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer abortCancel()
	abortCmd := safeexec.CommandContext(abortCtx, "git", "-C", worktreePath, "merge", "--abort")
	if abortOut, abortErr := abortCmd.CombinedOutput(); abortErr != nil {
		return nil, fmt.Errorf("merge of %s conflicted in %v, and merge --abort failed: %s (%w)", mainBranch, conflictFiles, abortOut, abortErr)
	}

	return &MergeMainResult{Conflicted: true, ConflictedFiles: conflictFiles}, nil
}

// conflictedFiles returns the paths with unresolved merge conflicts in worktreePath.
func conflictedFiles(worktreePath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := safeexec.CommandContext(ctx, "git", "-C", worktreePath, "diff", "--name-only", "--diff-filter=U")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
