"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { useSelector } from "react-redux";
import { selectAllSessions } from "@/lib/store/sessionsSlice";
import type { RootState } from "@/lib/store/store";
import { GitHubAuthError, type GitHubRepo, type GitHubIssue } from "./useBacklogService";
import {
  getCachedRepos,
  setCachedRepos,
  getCachedIssues,
  setCachedIssues,
  getLastUsedRepo,
  setLastUsedRepo,
} from "@/lib/utils/issuePickerCache";

// ─── Types ───────────────────────────────────────────────────────────────────

export type PickerPhase = "repo" | "issue";

export interface UseGitHubIssuePickerOptions {
  searchGitHubRepos: (query: string, limit?: number) => Promise<GitHubRepo[]>;
  listGitHubIssues: (
    owner: string,
    repo: string,
    options?: { state?: string; search?: string; limit?: number }
  ) => Promise<GitHubIssue[]>;
  onSelect: (owner: string, repo: string, issue: GitHubIssue) => void;
}

export interface UseGitHubIssuePickerReturn {
  phase: PickerPhase;
  // Repo phase
  repoQuery: string;
  repos: GitHubRepo[];
  reposLoading: boolean;
  selectedRepo: GitHubRepo | null;
  setRepoQuery: (q: string) => void;
  selectRepo: (repo: GitHubRepo) => void;
  // Issue phase
  issueSearch: string;
  issueState: "open" | "closed" | "all";
  issues: GitHubIssue[];
  issuesLoading: boolean;
  setIssueSearch: (s: string) => void;
  setIssueState: (s: "open" | "closed" | "all") => void;
  selectIssue: (issue: GitHubIssue) => void;
  // Two-level Escape: back to repo selection
  goBack: () => void;
  // Auth
  authError: boolean;
  reset: () => void;
}

// ─── Hook ────────────────────────────────────────────────────────────────────

const DEBOUNCE_MS = 150;

export function useGitHubIssuePicker({
  searchGitHubRepos,
  listGitHubIssues,
  onSelect,
}: UseGitHubIssuePickerOptions): UseGitHubIssuePickerReturn {
  const [phase, setPhase] = useState<PickerPhase>("repo");
  const [repoQuery, setRepoQuery] = useState("");
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [reposLoading, setReposLoading] = useState(false);
  const [selectedRepo, setSelectedRepo] = useState<GitHubRepo | null>(null);
  const [issueSearch, setIssueSearch] = useState("");
  const [issueState, setIssueState] = useState<"open" | "closed" | "all">("open");
  const [issues, setIssues] = useState<GitHubIssue[]>([]);
  const [issuesLoading, setIssuesLoading] = useState(false);
  const [authError, setAuthError] = useState(false);

  // Generation counter prevents stale async responses from overwriting newer results.
  const repoGenRef = useRef(0);
  const issueGenRef = useRef(0);

  // Pull local-path repos from Redux session list for the "local repos" tier.
  const localRepos = useSelector((state: RootState) => {
    const sessions = selectAllSessions(state);
    const seen = new Set<string>();
    const results: GitHubRepo[] = [];
    for (const s of sessions) {
      if (!s.path) continue;
      const parts = s.path.split("/");
      const repo = parts[parts.length - 1] ?? "";
      const owner = parts[parts.length - 2] ?? "";
      const key = `${owner}/${repo}`;
      if (seen.has(key)) continue;
      seen.add(key);
      results.push({ owner, repo, isLocal: true, localPath: s.path, description: s.title ?? "" });
    }
    return results;
  });

  // ─── Repo phase: fetch on query change ─────────────────────────────────────

  useEffect(() => {
    if (phase !== "repo") return;

    const gen = ++repoGenRef.current;

    // If empty query, show cached repos or local repos immediately.
    if (repoQuery === "") {
      const cached = getCachedRepos();
      if (cached && cached.length > 0) {
        if (gen === repoGenRef.current) {
          setRepos(
            cached.map((r) => ({ ...r, isLocal: false, localPath: "" }))
          );
        }
        return;
      }
    }

    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | null = null;

    timer = setTimeout(async () => {
      if (gen !== repoGenRef.current) return;
      setReposLoading(true);
      try {
        const results = await searchGitHubRepos(repoQuery, 30);
        if (gen !== repoGenRef.current) return;
        setRepos(results);
        if (repoQuery === "") {
          setCachedRepos(
            results.map((r) => ({
              owner: r.owner,
              repo: r.repo,
              description: r.description,
            }))
          );
        }
        setAuthError(false);
      } catch (err) {
        if (gen !== repoGenRef.current) return;
        if (err instanceof GitHubAuthError) {
          setAuthError(true);
        }
      } finally {
        if (gen === repoGenRef.current) setReposLoading(false);
      }
    }, DEBOUNCE_MS);

    return () => {
      controller.abort();
      if (timer !== null) clearTimeout(timer);
    };
  }, [repoQuery, phase, searchGitHubRepos]);

  // Restore last-used repo on mount (repo phase only, first render).
  useEffect(() => {
    const last = getLastUsedRepo();
    if (last && phase === "repo" && !selectedRepo) {
      // Pre-populate as a hint; user must still confirm.
      setRepoQuery(`${last.owner}/${last.repo}`);
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ─── Issue phase: fetch on search/state/repo change ────────────────────────

  useEffect(() => {
    if (phase !== "issue" || !selectedRepo) return;

    const gen = ++issueGenRef.current;
    const { owner, repo } = selectedRepo;

    // Check cache (only for no-search case).
    if (issueSearch === "") {
      const cached = getCachedIssues(owner, repo, issueState);
      if (cached) {
        if (gen === issueGenRef.current) {
          setIssues(cached);
        }
        return;
      }
    }

    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | null = null;

    timer = setTimeout(async () => {
      if (gen !== issueGenRef.current) return;
      setIssuesLoading(true);
      try {
        const results = await listGitHubIssues(owner, repo, {
          state: issueState,
          search: issueSearch,
          limit: 50,
        });
        if (gen !== issueGenRef.current) return;
        setIssues(results);
        if (issueSearch === "") {
          setCachedIssues(owner, repo, issueState, results);
        }
        setAuthError(false);
      } catch (err) {
        if (gen !== issueGenRef.current) return;
        if (err instanceof GitHubAuthError) {
          setAuthError(true);
        }
      } finally {
        if (gen === issueGenRef.current) setIssuesLoading(false);
      }
    }, DEBOUNCE_MS);

    return () => {
      controller.abort();
      if (timer !== null) clearTimeout(timer);
    };
  }, [phase, selectedRepo, issueSearch, issueState, listGitHubIssues]);

  // ─── Actions ────────────────────────────────────────────────────────────────

  const selectRepo = useCallback((repo: GitHubRepo) => {
    setSelectedRepo(repo);
    setLastUsedRepo(repo.owner, repo.repo);
    setIssues([]);
    setIssueSearch("");
    setPhase("issue");
  }, []);

  const selectIssue = useCallback(
    (issue: GitHubIssue) => {
      if (!selectedRepo) return;
      onSelect(selectedRepo.owner, selectedRepo.repo, issue);
    },
    [selectedRepo, onSelect]
  );

  const goBack = useCallback(() => {
    setPhase("repo");
    setSelectedRepo(null);
    setIssues([]);
    setIssueSearch("");
  }, []);

  const reset = useCallback(() => {
    setPhase("repo");
    setRepoQuery("");
    setRepos([]);
    setSelectedRepo(null);
    setIssues([]);
    setIssueSearch("");
    setIssueState("open");
    setAuthError(false);
    repoGenRef.current = 0;
    issueGenRef.current = 0;
  }, []);

  return {
    phase,
    repoQuery,
    repos: phase === "repo" && repoQuery === "" ? [...localRepos, ...repos] : repos,
    reposLoading,
    selectedRepo,
    setRepoQuery,
    selectRepo,
    issueSearch,
    issueState,
    issues,
    issuesLoading,
    setIssueSearch,
    setIssueState,
    selectIssue,
    goBack,
    authError,
    reset,
  };
}
