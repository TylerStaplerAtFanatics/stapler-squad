// +feature: backlog:github-issue-picker
"use client";

import { useCallback, useRef, useEffect } from "react";
import { useBacklogService, type GitHubIssue } from "@/lib/hooks/useBacklogService";
import { useGitHubIssuePicker } from "@/lib/hooks/useGitHubIssuePicker";
import * as styles from "./GitHubIssuePicker.css";

// ─── Props ────────────────────────────────────────────────────────────────────

interface GitHubIssuePickerProps {
  onSelect: (owner: string, repo: string, issue: GitHubIssue) => void;
  onCancel: () => void;
}

// ─── Component ────────────────────────────────────────────────────────────────

export function GitHubIssuePicker({ onSelect, onCancel }: GitHubIssuePickerProps) {
  const { searchGitHubRepos, listGitHubIssues } = useBacklogService();

  const picker = useGitHubIssuePicker({ searchGitHubRepos, listGitHubIssues, onSelect });

  const searchRef = useRef<HTMLInputElement>(null);

  // Focus input when phase changes.
  useEffect(() => {
    searchRef.current?.focus();
  }, [picker.phase]);

  // Two-level Escape: issue → repo → onCancel.
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      if (picker.phase === "issue") {
        picker.goBack();
      } else {
        onCancel();
      }
    },
    [picker, onCancel]
  );

  if (picker.authError) {
    return (
      <div className={styles.container}>
        <div className={styles.authErrorBox}>
          No GitHub token configured. Set <code>GITHUB_TOKEN</code> or <code>GH_TOKEN</code> to
          enable the GitHub issue picker.
        </div>
        <div style={{ display: "flex", justifyContent: "flex-end" }}>
          <button type="button" onClick={onCancel} className={styles.backButton}>
            Close
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.container} onKeyDown={handleKeyDown}>
      {picker.phase === "repo" ? (
        <RepoPhase picker={picker} searchRef={searchRef} />
      ) : (
        <IssuePhase picker={picker} searchRef={searchRef} />
      )}
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button type="button" onClick={onCancel} className={styles.backButton}>
          Cancel
        </button>
      </div>
    </div>
  );
}

// ─── Repo phase ───────────────────────────────────────────────────────────────

function RepoPhase({
  picker,
  searchRef,
}: {
  picker: ReturnType<typeof useGitHubIssuePicker>;
  searchRef: React.RefObject<HTMLInputElement | null>;
}) {
  return (
    <>
      <div className={styles.phaseHeader}>
        <span style={{ fontSize: "13px", fontWeight: 600 }}>Select a repository</span>
      </div>
      <input
        ref={searchRef}
        className={styles.searchInput}
        type="text"
        placeholder="Search repos…"
        value={picker.repoQuery}
        onChange={(e) => picker.setRepoQuery(e.target.value)}
        aria-label="Search GitHub repositories"
        aria-autocomplete="list"
        aria-controls="repo-list"
        autoComplete="off"
        autoFocus
      />
      <div id="repo-list" role="listbox" aria-label="GitHub repositories" className={styles.listContainer}>
        {picker.reposLoading ? (
          <div className={styles.loadingText}>Loading…</div>
        ) : picker.repos.length === 0 ? (
          <div className={styles.emptyState}>
            {picker.repoQuery ? "No repos found." : "No repos available."}
          </div>
        ) : (
          picker.repos.map((repo) => (
            <div
              key={`${repo.owner}/${repo.repo}`}
              role="option"
              aria-selected={false}
              className={styles.listItem}
              onMouseDown={(e) => {
                // Prevent onBlur on the input before click registers.
                e.preventDefault();
                picker.selectRepo(repo);
              }}
            >
              <span className={styles.listItemName}>
                {repo.owner}/{repo.repo}
              </span>
              {repo.isLocal && <span className={styles.localBadge}>local</span>}
              {repo.description && (
                <span className={styles.listItemMeta}>{repo.description}</span>
              )}
            </div>
          ))
        )}
      </div>
    </>
  );
}

// ─── Issue phase ──────────────────────────────────────────────────────────────

function IssuePhase({
  picker,
  searchRef,
}: {
  picker: ReturnType<typeof useGitHubIssuePicker>;
  searchRef: React.RefObject<HTMLInputElement | null>;
}) {
  const states: Array<"open" | "closed" | "all"> = ["open", "closed", "all"];

  return (
    <>
      <div className={styles.phaseHeader}>
        <button
          type="button"
          className={styles.backButton}
          onClick={picker.goBack}
          aria-label="Back to repository selection"
        >
          ← Back
        </button>
        <span className={styles.repoChip} aria-label="Selected repository">
          {picker.selectedRepo?.owner}/{picker.selectedRepo?.repo}
        </span>
      </div>

      <div className={styles.filterBar}>
        <input
          ref={searchRef}
          className={styles.searchInput}
          type="search"
          placeholder="Search issues…"
          value={picker.issueSearch}
          onChange={(e) => picker.setIssueSearch(e.target.value)}
          aria-label="Search issues"
          autoComplete="off"
          style={{ flex: 1 }}
          autoFocus
        />
        <div className={styles.stateToggle} role="group" aria-label="Issue state filter">
          {states.map((s) => (
            <button
              key={s}
              type="button"
              className={picker.issueState === s ? styles.stateButton.active : styles.stateButton.inactive}
              onMouseDown={(e) => {
                e.preventDefault();
                picker.setIssueState(s);
              }}
            >
              {s}
            </button>
          ))}
        </div>
      </div>

      <div role="listbox" aria-label="GitHub issues" className={styles.listContainer}>
        {picker.issuesLoading ? (
          <div className={styles.loadingText}>Loading…</div>
        ) : picker.issues.length === 0 ? (
          <div className={styles.emptyState}>
            {picker.issueSearch ? "No issues match your search." : "No issues found."}
          </div>
        ) : (
          picker.issues.map((issue) => (
            <IssueRow key={issue.number} issue={issue} onSelect={picker.selectIssue} />
          ))
        )}
      </div>
    </>
  );
}

// ─── Issue row ────────────────────────────────────────────────────────────────

function IssueRow({ issue, onSelect }: { issue: GitHubIssue; onSelect: (i: GitHubIssue) => void }) {
  return (
    <div
      role="option"
      aria-selected={false}
      className={styles.listItem}
      onMouseDown={(e) => {
        e.preventDefault();
        onSelect(issue);
      }}
    >
      <span className={styles.issueNumber}>#{issue.number}</span>
      <span className={styles.listItemName}>{issue.title}</span>
      {issue.labels.slice(0, 2).map((label) => (
        <span key={label} className={styles.labelBadge} title={label}>
          {label}
        </span>
      ))}
    </div>
  );
}
