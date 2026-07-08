package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/tstapler/stapler-squad/executor/safeexec"
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
