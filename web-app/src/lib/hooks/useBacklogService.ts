"use client";

import { useCallback, useRef, useEffect, useState, useMemo } from "react";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { getApiBaseUrl, createAuthInterceptor } from "@/lib/config";
import {
  BacklogService,
  BacklogItem as BacklogItemProto,
  AcCriterion as AcCriterionProto,
  ItemSession as ItemSessionProto,
  TriageTask as TriageTaskProto,
  BacklogStatusEvent as BacklogStatusEventProto,
} from "@/gen/session/v1/backlog_pb";

// ---------------------------------------------------------------------------
// Domain types exposed to UI (mapped from proto, but without Message<> noise)
// ---------------------------------------------------------------------------

export type KnownBacklogStatus = "idea" | "refining" | "ready" | "in_progress" | "review" | "pr_pending" | "done" | "archived";
// (string & {}) preserves autocomplete for KnownBacklogStatus values while still
// accepting unknown statuses returned by newer server versions.
export type BacklogItemStatus = KnownBacklogStatus | (string & {});

export type AcCriterionStatus = "pending" | "in_progress" | "done";

export interface AcCriterion {
  index: number;
  text: string;
  status: AcCriterionStatus;
}

export interface TriageSuggestion {
  text: string;
  rationale: string; // "question" for R7-lite clarifying questions
}

export interface TriageTask {
  text: string;
  estimate: string;
  category: string;
}

export interface TriageResult {
  summary: string;
  suggestions: TriageSuggestion[];
  clarifyingQuestions: string[];
  tasks?: TriageTask[];
  /** 1 for the initial triage run, incrementing for each feedback-driven refine. */
  iteration?: number;
  /** Feedback text that produced this iteration, empty for the initial run. */
  feedback?: string;
}

export interface LinkedSession {
  /** Entity UUID of the ItemSession record — use for overrideVerdict calls. */
  entityId: string;
  /** Tmux session UUID — use for linking to the session terminal. */
  sessionId: string;
  role: string;
  startedAt?: string;
  endedAt?: string;
  reviewVerdict?: {
    overallOutcome?: "PASS" | "PARTIAL" | "FAIL" | "PENDING" | "UNVERIFIABLE";
    summary?: string;
    perCriterion?: Array<{ criterionIndex: number; outcome: string; evidence: string }>;
  };
  triageResult?: TriageResult;
  estimatedCostUsd: number;
  /** Git branch name for the session's worktree, if one exists. */
  worktreeBranch?: string;
}

export interface BacklogItem {
  id: string;
  title: string;
  description?: string;
  status: BacklogItemStatus;
  /** 1 = highest priority, 5 = lowest */
  priority: number;
  repoPath?: string;
  skipPlanning: boolean;
  skipReviewGate: boolean;
  planApproved: boolean;
  planArtifactsPath?: string;
  acCriteria: AcCriterion[];
  linkedSessions: LinkedSession[];
  notes?: string;
  createdAt?: string;
  updatedAt?: string;
  /** Gate verdict from the most recent item session (if in review status) */
  gateVerdict?: "PASS" | "PARTIAL" | "FAIL" | "PENDING" | "UNVERIFIABLE";
  gateVerdictSummary?: string;
  gateCriteria?: Array<{ label: string; passed: boolean }>;
  /** Triage progress indicator: when item is in "idea" status being triaged */
  triageStatus?: "running" | "completed" | "failed";
  /** Triage result from the most recent triage session (populated when triageStatus === "completed") */
  triageResult?: TriageResult;
  /** Status transition history for this item (audit log) */
  statusEvents: StatusEvent[];
  /** Sum of estimated USD cost across all linked sessions */
  totalEstimatedCostUsd: number;
  /** GitHub PR URL when item is in pr_pending status */
  prUrl?: string;
  /** GitHub PR number when item is in pr_pending status */
  prNumber?: number;
}

export interface StatusEvent {
  id: string;
  fromStatus: string;
  toStatus: string;
  triggeredBy: string;
  createdAt?: string;
}

export interface BacklogItemInput {
  title: string;
  description?: string;
  repoPath?: string;
  priority?: number;
  skipPlanning?: boolean;
  skipReviewGate?: boolean;
  acCriteria?: AcCriterion[];
  notes?: string;
  skipTriage?: boolean;
}

export interface ListBacklogItemsFilter {
  statuses?: BacklogItemStatus[];
  priorities?: number[];
  includeTerminal?: boolean;
  search?: string;
}

// ---------------------------------------------------------------------------
// Proto ↔ domain mapping helpers
// ---------------------------------------------------------------------------

function mapAcCriterion(c: AcCriterionProto): AcCriterion {
  return {
    index: c.index,
    text: c.text,
    status: (c.status || "pending") as AcCriterionStatus,
  };
}

function mapItemSession(s: ItemSessionProto): LinkedSession {
  const session: LinkedSession = {
    entityId: s.id,
    sessionId: s.sessionUuid,
    role: s.sessionRole,
    startedAt: s.startedAt ? new Date(Number(s.startedAt.seconds) * 1000).toISOString() : undefined,
    endedAt: s.endedAt ? new Date(Number(s.endedAt.seconds) * 1000).toISOString() : undefined,
    estimatedCostUsd: s.estimatedCostUsd ?? 0,
    worktreeBranch: s.worktreeBranch || undefined,
  };

  // Map review verdict if present
  if (s.reviewVerdict) {
    const rv = s.reviewVerdict;
    const knownOutcomes = new Set(["PASS", "FAIL", "PARTIAL", "UNVERIFIABLE"]);
    session.reviewVerdict = {
      overallOutcome: knownOutcomes.has(rv.overallOutcome)
        ? (rv.overallOutcome as "PASS" | "PARTIAL" | "FAIL" | "PENDING" | "UNVERIFIABLE")
        : rv.overallOutcome
          ? "PARTIAL"
          : "PENDING",
      summary: rv.summary,
      perCriterion: (rv.perCriterion ?? []).map((c) => ({
        criterionIndex: c.criterionIndex,
        outcome: c.outcome,
        evidence: c.evidence,
      })),
    };
  }

  // Map triage result if present
  if (s.triageResult) {
    const tr = s.triageResult;
    session.triageResult = {
      summary: tr.summary,
      suggestions: (tr.suggestions ?? []).map((sg) => ({
        text: sg.text,
        rationale: sg.rationale,
      })),
      clarifyingQuestions: tr.clarifyingQuestions ?? [],
      tasks: (tr.tasks ?? []).map((t: TriageTaskProto) => ({
        text: t.text,
        estimate: t.estimate,
        category: t.category,
      })),
      iteration: tr.iteration,
      feedback: tr.feedback,
    };
  }

  return session;
}

function mapStatusEvent(e: BacklogStatusEventProto): StatusEvent {
  return {
    id: e.id,
    fromStatus: e.fromStatus,
    toStatus: e.toStatus,
    triggeredBy: e.triggeredBy,
    createdAt: e.createdAt ? new Date(Number(e.createdAt.seconds) * 1000).toISOString() : undefined,
  };
}

function mapBacklogItem(p: BacklogItemProto): BacklogItem {
  const linkedSessions = (p.itemSessions ?? []).map(mapItemSession);

  // Extract gate verdict from the most recent session (for review status)
  let gateVerdict: "PASS" | "PARTIAL" | "FAIL" | "PENDING" | "UNVERIFIABLE" | undefined;
  let gateVerdictSummary: string | undefined;
  let gateCriteria: Array<{ label: string; passed: boolean }> | undefined;

  // Use the most recent review session's verdict — not the most recent session of any
  // role, which could be a work session (no verdict) after a reopen-for-revision cycle.
  const mostRecentReviewSession = linkedSessions.filter((s) => s.role === "review").at(-1);
  if (mostRecentReviewSession?.reviewVerdict?.overallOutcome) {
    gateVerdict = mostRecentReviewSession.reviewVerdict.overallOutcome;
    gateVerdictSummary = mostRecentReviewSession.reviewVerdict.summary;

    if (mostRecentReviewSession.reviewVerdict.perCriterion?.length) {
      gateCriteria = mostRecentReviewSession.reviewVerdict.perCriterion.map((c) => ({
        label: c.evidence ? `${c.outcome}: ${c.evidence}` : `Criterion ${c.criterionIndex}: ${c.outcome}`,
        passed: c.outcome === "PASS" || c.outcome === "pass",
      }));
    }
  }

  // Derive triageStatus from linked sessions.
  // P12 fix: only mark "completed" if the session ended AND has a non-empty summary.
  // Orphan detection: a triage session without endedAt is only "running" while the item
  // is in "idea" status. If the item has advanced (ready, in_progress, etc.) the session
  // died without cleanly recording its end — treat it as "failed" so the UI doesn't show
  // a loading indicator for a session that no longer exists.
  const itemStatus = (p.status || "idea") as BacklogItemStatus;
  let triageStatus: BacklogItem["triageStatus"];
  const triageSession = linkedSessions.filter((s) => s.role === "triage").at(-1);
  if (triageSession) {
    if (triageSession.endedAt) {
      triageStatus = triageSession.triageResult?.summary ? "completed" : "failed";
    } else if (itemStatus === "idea") {
      triageStatus = "running";
    } else {
      triageStatus = "failed";
    }
  }

  const triageResult = triageSession?.triageResult;

  return {
    id: p.id,
    title: p.title,
    description: p.description || undefined,
    status: (p.status || "idea") as BacklogItemStatus,
    priority: p.priority || 3,
    repoPath: p.repoPath || undefined,
    skipPlanning: p.skipPlanning,
    skipReviewGate: p.skipReviewGate,
    planApproved: p.planApproved,
    planArtifactsPath: p.planArtifactsPath || undefined,
    acCriteria: (p.acceptanceCriteria ?? []).map(mapAcCriterion),
    linkedSessions,
    notes: p.notes || undefined,
    createdAt: p.createdAt ? new Date(Number(p.createdAt.seconds) * 1000).toISOString() : undefined,
    updatedAt: p.updatedAt ? new Date(Number(p.updatedAt.seconds) * 1000).toISOString() : undefined,
    gateVerdict,
    gateVerdictSummary,
    gateCriteria,
    triageStatus,
    triageResult,
    statusEvents: (p.statusEvents ?? []).map(mapStatusEvent),
    totalEstimatedCostUsd: p.totalEstimatedCostUsd ?? 0,
    prUrl: p.prUrl || undefined,
    prNumber: p.prNumber || undefined,
  };
}

function toProtoAcCriteria(criteria: AcCriterion[]): AcCriterionProto[] {
  return criteria.map((c) => ({
    $typeName: "session.v1.AcCriterion" as const,
    index: c.index,
    text: c.text,
    status: c.status,
  }));
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GitHub picker domain types
// ---------------------------------------------------------------------------

export interface GitHubRepo {
  owner: string;
  repo: string;
  isLocal: boolean;
  localPath: string;
  description: string;
}

export interface GitHubIssue {
  number: number;
  title: string;
  state: string;
  url: string;
  labels: string[];
}

export class GitHubAuthError extends Error {
  constructor() {
    super("No GitHub token configured");
    this.name = "GitHubAuthError";
  }
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

interface UseBacklogServiceReturn {
  listBacklogItems: (filter?: ListBacklogItemsFilter) => Promise<BacklogItem[]>;
  getBacklogItem: (id: string) => Promise<BacklogItem | null>;
  createBacklogItem: (data: BacklogItemInput) => Promise<{ item: BacklogItem; triageTriggered: boolean } | null>;
  importGitHubIssue: (issueUrl: string, options?: { repoPath?: string; skipPlanning?: boolean }) => Promise<{ item: BacklogItem; triageTriggered: boolean } | null>;
  searchGitHubRepos: (query: string, limit?: number) => Promise<GitHubRepo[]>;
  listGitHubIssues: (owner: string, repo: string, options?: { state?: string; search?: string; limit?: number }) => Promise<GitHubIssue[]>;
  updateBacklogItem: (id: string, data: Partial<BacklogItemInput>) => Promise<BacklogItem | null>;
  archiveBacklogItem: (id: string) => Promise<boolean>;
  deleteBacklogItem: (id: string) => Promise<boolean>;
  transitionStatus: (
    id: string,
    toStatus: BacklogItemStatus,
    precondition?: BacklogItemStatus
  ) => Promise<BacklogItem | null>;
  spawnSessionFromItem: (id: string, options?: { autonomous?: boolean; force?: boolean }) => Promise<{ sessionUuid: string } | null>;
  triggerTriage: (id: string, feedback?: string) => Promise<{ itemSessionId: string } | null>;
  cancelTriage: (id: string) => Promise<boolean>;
  approvePlan: (id: string) => Promise<BacklogItem | null>;
  overrideVerdict: (id: string, overrideReason: string, toStatus?: string) => Promise<boolean>;
  triggerReReview: (id: string) => Promise<boolean>;
  /** Last error from createBacklogItem, updateBacklogItem, transitionStatus, or spawnSessionFromItem. */
  lastError: Error | null;
  /** Clears the lastError state. */
  clearError: () => void;
}

export function useBacklogService(): UseBacklogServiceReturn {
  const clientRef = useRef<ReturnType<typeof createClient<typeof BacklogService>> | null>(null);
  const [lastError, setLastError] = useState<Error | null>(null);

  const clearError = useCallback(() => setLastError(null), []);

  useEffect(() => {
    const transport = createConnectTransport({
      baseUrl: getApiBaseUrl(),
      interceptors: [createAuthInterceptor()],
    });
    clientRef.current = createClient(BacklogService, transport);
  }, []);

  const listBacklogItems = useCallback(
    async (filter?: ListBacklogItemsFilter): Promise<BacklogItem[]> => {
      if (!clientRef.current) return [];
      try {
        const resp = await clientRef.current.listBacklogItems({
          status: filter?.statuses ?? [],
          priority: filter?.priorities ?? [],
          includeTerminal: filter?.includeTerminal ?? false,
          sortBy: "",
        });
        const items = (resp.items ?? []).map(mapBacklogItem);
        if (filter?.search) {
          const q = filter.search.toLowerCase();
          return items.filter(
            (item) =>
              item.title.toLowerCase().includes(q) ||
              item.description?.toLowerCase().includes(q)
          );
        }
        return items;
      } catch (err) {
        console.error("[useBacklogService] listBacklogItems:", err);
        return [];
      }
    },
    []
  );

  const getBacklogItem = useCallback(async (id: string): Promise<BacklogItem | null> => {
    if (!clientRef.current) return null;
    try {
      const resp = await clientRef.current.getBacklogItem({ itemId: id });
      return resp.item ? mapBacklogItem(resp.item) : null;
    } catch (err) {
      console.error("[useBacklogService] getBacklogItem:", err);
      return null;
    }
  }, []);

  const createBacklogItem = useCallback(
    async (data: BacklogItemInput): Promise<{ item: BacklogItem; triageTriggered: boolean } | null> => {
      if (!clientRef.current) return null;
      try {
        setLastError(null);
        const resp = await clientRef.current.createBacklogItem({
          title: data.title,
          description: data.description ?? "",
          repoPath: data.repoPath ?? "",
          priority: data.priority ?? 3,
          skipPlanning: data.skipPlanning ?? false,
          skipReviewGate: data.skipReviewGate ?? false,
          acceptanceCriteria: toProtoAcCriteria(data.acCriteria ?? []),
          notes: data.notes ?? "",
          skipTriage: data.skipTriage ?? false,
        });
        return resp.item
          ? { item: mapBacklogItem(resp.item), triageTriggered: resp.triageTriggered }
          : null;
      } catch (err) {
        console.error("[useBacklogService] createBacklogItem:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        return null;
      }
    },
    []
  );

  const updateBacklogItem = useCallback(
    async (id: string, data: Partial<BacklogItemInput>): Promise<BacklogItem | null> => {
      if (!clientRef.current) return null;
      try {
        setLastError(null);
        const resp = await clientRef.current.updateBacklogItem({
          itemId: id,
          title: data.title,
          description: data.description,
          repoPath: data.repoPath,
          priority: data.priority,
          skipPlanning: data.skipPlanning,
          skipReviewGate: data.skipReviewGate,
          acceptanceCriteria: data.acCriteria ? toProtoAcCriteria(data.acCriteria) : undefined,
          notes: data.notes,
        });
        return resp.item ? mapBacklogItem(resp.item) : null;
      } catch (err) {
        console.error("[useBacklogService] updateBacklogItem:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        return null;
      }
    },
    []
  );

  const archiveBacklogItem = useCallback(async (id: string): Promise<boolean> => {
    if (!clientRef.current) return false;
    try {
      await clientRef.current.archiveBacklogItem({ itemId: id });
      return true;
    } catch (err) {
      console.error("[useBacklogService] archiveBacklogItem:", err);
      setLastError(err instanceof Error ? err : new Error(String(err)));
      throw err;
    }
  }, []);

  const deleteBacklogItem = useCallback(async (id: string): Promise<boolean> => {
    if (!clientRef.current) return false;
    try {
      await clientRef.current.deleteBacklogItem({ itemId: id });
      return true;
    } catch (err) {
      console.error("[useBacklogService] deleteBacklogItem:", err);
      setLastError(err instanceof Error ? err : new Error(String(err)));
      throw err;
    }
  }, []);

  const transitionStatus = useCallback(
    async (
      id: string,
      toStatus: BacklogItemStatus,
      precondition?: BacklogItemStatus
    ): Promise<BacklogItem | null> => {
      if (!clientRef.current) return null;
      try {
        setLastError(null);
        const resp = await clientRef.current.transitionBacklogItemStatus({
          itemId: id,
          targetStatus: toStatus,
          expectedStatus: precondition ?? "",
          overrideReason: "",
        });
        return resp.item ? mapBacklogItem(resp.item) : null;
      } catch (err) {
        console.error("[useBacklogService] transitionStatus:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        return null;
      }
    },
    []
  );

  const spawnSessionFromItem = useCallback(
    async (id: string, options?: { autonomous?: boolean; force?: boolean }): Promise<{ sessionUuid: string } | null> => {
      if (!clientRef.current) return null;
      try {
        setLastError(null);
        const resp = await clientRef.current.spawnSessionFromItem({
          itemId: id,
          autonomous: options?.autonomous ?? false,
          force: options?.force ?? false,
        });
        return { sessionUuid: resp.sessionUuid };
      } catch (err) {
        console.error("[useBacklogService] spawnSessionFromItem:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        return null;
      }
    },
    []
  );

  const triggerTriage = useCallback(
    async (id: string, feedback?: string): Promise<{ itemSessionId: string } | null> => {
      if (!clientRef.current) return null;
      try {
        const resp = await clientRef.current.triggerTriage({ itemId: id, feedback: feedback ?? "" });
        return { itemSessionId: resp.itemSession?.id ?? "" };
      } catch (err) {
        console.error("[useBacklogService] triggerTriage:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        throw err;
      }
    },
    []
  );

  const cancelTriage = useCallback(async (id: string): Promise<boolean> => {
    if (!clientRef.current) return false;
    try {
      const resp = await clientRef.current.cancelTriage({ itemId: id });
      return resp.cancelled;
    } catch (err) {
      console.error("[useBacklogService] cancelTriage:", err);
      setLastError(err instanceof Error ? err : new Error(String(err)));
      return false;
    }
  }, []);

  const approvePlan = useCallback(async (id: string): Promise<BacklogItem | null> => {
    if (!clientRef.current) return null;
    try {
      const resp = await clientRef.current.approvePlan({ itemId: id });
      return resp.item ? mapBacklogItem(resp.item) : null;
    } catch (err) {
      console.error("[useBacklogService] approvePlan:", err);
      setLastError(err instanceof Error ? err : new Error(String(err)));
      throw err;
    }
  }, []);

  const overrideVerdict = useCallback(
    async (id: string, overrideReason: string, toStatus?: string): Promise<boolean> => {
      if (!clientRef.current) return false;
      try {
        await clientRef.current.overrideVerdict({
          itemSessionId: id,
          overrideReason,
          toStatus: toStatus ?? "done",
        });
        return true;
      } catch (err) {
        console.error("[useBacklogService] overrideVerdict:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        throw err;
      }
    },
    []
  );

  const triggerReReview = useCallback(async (id: string): Promise<boolean> => {
    if (!clientRef.current) return false;
    try {
      await clientRef.current.triggerReReview({ itemId: id });
      return true;
    } catch (err) {
      console.error("[useBacklogService] triggerReReview:", err);
      setLastError(err instanceof Error ? err : new Error(String(err)));
      throw err;
    }
  }, []);

  const importGitHubIssue = useCallback(
    async (
      issueUrl: string,
      options?: { repoPath?: string; skipPlanning?: boolean }
    ): Promise<{ item: BacklogItem; triageTriggered: boolean } | null> => {
      if (!clientRef.current) return null;
      try {
        setLastError(null);
        const resp = await clientRef.current.importGitHubIssue({
          issueUrl,
          repoPath: options?.repoPath ?? "",
          skipPlanning: options?.skipPlanning ?? false,
        });
        return resp.item
          ? { item: mapBacklogItem(resp.item), triageTriggered: resp.triageTriggered }
          : null;
      } catch (err) {
        console.error("[useBacklogService] importGitHubIssue:", err);
        setLastError(err instanceof Error ? err : new Error(String(err)));
        return null;
      }
    },
    []
  );

  const searchGitHubRepos = useCallback(
    async (query: string, limit?: number): Promise<GitHubRepo[]> => {
      if (!clientRef.current) return [];
      try {
        const resp = await clientRef.current.searchGitHubRepos({ query, limit: limit ?? 30 });
        return resp.repos.map((r) => ({
          owner: r.owner,
          repo: r.repo,
          isLocal: r.isLocal,
          localPath: r.localPath,
          description: r.description,
        }));
      } catch (err) {
        if (err instanceof Error && err.message.toLowerCase().includes("token")) {
          throw new GitHubAuthError();
        }
        throw err;
      }
    },
    []
  );

  const listGitHubIssues = useCallback(
    async (
      owner: string,
      repo: string,
      options?: { state?: string; search?: string; limit?: number }
    ): Promise<GitHubIssue[]> => {
      if (!clientRef.current) return [];
      try {
        const resp = await clientRef.current.listGitHubIssues({
          owner,
          repo,
          state: options?.state ?? "open",
          search: options?.search ?? "",
          limit: options?.limit ?? 30,
        });
        return resp.issues.map((i) => ({
          number: i.number,
          title: i.title,
          state: i.state,
          url: i.url,
          labels: i.labels,
        }));
      } catch (err) {
        if (err instanceof Error && err.message.toLowerCase().includes("token")) {
          throw new GitHubAuthError();
        }
        throw err;
      }
    },
    []
  );

  // Stable object reference: all methods are useCallback(fn,[]) — only lastError changes.
  // Without useMemo, every render creates a new object, making callers' useCallback deps
  // fire on every render and causing infinite reload loops.
  return useMemo(
    () => ({
      listBacklogItems,
      getBacklogItem,
      createBacklogItem,
      importGitHubIssue,
      searchGitHubRepos,
      listGitHubIssues,
      updateBacklogItem,
      archiveBacklogItem,
      deleteBacklogItem,
      transitionStatus,
      spawnSessionFromItem,
      triggerTriage,
      cancelTriage,
      approvePlan,
      overrideVerdict,
      triggerReReview,
      lastError,
      clearError,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [lastError]
  );
}

// ---------------------------------------------------------------------------
// Backlog session index — maps tmux session UUIDs to backlog item metadata
// ---------------------------------------------------------------------------

export interface BacklogIndexEntry {
  itemId: string;
  itemTitle: string;
  itemStatus: string;
  sessionRole: string;
}

export interface UseBacklogSessionIndexReturn {
  index: Map<string, BacklogIndexEntry>;
  loading: boolean;
}

/**
 * Fetches the full session→backlog index once on mount.
 * Returns a stable Map keyed by tmux session UUID.
 */
export function useBacklogSessionIndex(): UseBacklogSessionIndexReturn {
  const [index, setIndex] = useState<Map<string, BacklogIndexEntry>>(new Map());
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const transport = createConnectTransport({
      baseUrl: getApiBaseUrl(),
      interceptors: [createAuthInterceptor()],
    });
    const client = createClient(BacklogService, transport);

    let cancelled = false;
    client
      .getSessionBacklogIndex({})
      .then((resp) => {
        if (cancelled) return;
        const map = new Map<string, BacklogIndexEntry>();
        for (const e of resp.entries ?? []) {
          if (e.sessionUuid) {
            map.set(e.sessionUuid, {
              itemId: e.itemId,
              itemTitle: e.itemTitle,
              itemStatus: e.itemStatus,
              sessionRole: e.sessionRole,
            });
          }
        }
        setIndex(map);
      })
      .catch((err) => {
        console.error("[useBacklogSessionIndex] failed:", err);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return { index, loading };
}
