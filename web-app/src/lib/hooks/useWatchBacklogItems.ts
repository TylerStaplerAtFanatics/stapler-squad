"use client";

// useWatchBacklogItems.ts — shared real-time subscription hook for backlog
// items (Epic 4.2, project_plans/backlog-event-driven-updates). Structurally
// mirrors useReviewQueue.ts's connection lifecycle (AbortController-per-
// effect-run, exponential backoff, REST fallback polling) and additionally
// ports useSessionService.ts's idle-staleness backstop (30s periodic timer +
// 15s visibility/online check) plus new after_seq-based forward/backward
// gap detection that neither existing hook has (pitfalls.md #4).
//
// Design notes on the two open questions this epic resolved:
//
// 1. item_archived events are intentionally NOT translated into a
//    backlogItemsSlice action here. BacklogItemArchivedEvent carries only
//    itemId/archivedAt — no full BacklogItem payload — so it cannot call
//    upsertItem, and plan.md's Task 4.2.1b oneof-to-action mapping omits it
//    on purpose. It is consumed at the component layer instead (Phase 5
//    Tasks 5.3.1c/5.4.1c) via a separate item-scoped subscription, not this
//    shared list-level hook or the normalized store.
//
// 2. connectionState is hook-local React state, not a backlogItemsSlice
//    reducer. Unlike sessionsSlice.connectionState (which useSessionService
//    reads via a Redux selector), no Epic 4.2 task lists backlogItemsSlice.ts
//    as a touched file for connectionState, and Task 4.2.1a's signature
//    returns connectionState directly from the hook's own return value.
//
// 3. Epic 5.2 fix (found blocking Phase 5 consumer wiring, both this board
//    and the /backlog list page): backlogItemsSlice stores the raw proto
//    BacklogItem (from @/gen/session/v1/backlog_pb) — acceptanceCriteria,
//    autoCreatePr, proto Timestamp fields — but every rendering consumer
//    (BacklogItemCard, BacklogBoard, BacklogItemDetail) is written against
//    useBacklogService.ts's mapped domain BacklogItem (acCriteria,
//    gateVerdict, triageStatus derived from itemSessions, ISO date strings).
//    Neither type is assignable to the other. This hook now maps through
//    useBacklogService's mapBacklogItem before returning items, so the
//    normalized store itself stays proto-shaped (unaffected) while every
//    consumer of this hook gets the domain shape it actually renders.
export type BacklogConnectionState =
  | "connecting"
  | "live"
  | "reconnecting"
  | "polling"
  | "stale";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { getApiBaseUrl, createAuthInterceptor } from "@/lib/config";
import { BacklogService } from "@/gen/session/v1/backlog_pb";
import type { BacklogItem, BacklogItemEvent } from "@/gen/session/v1/backlog_pb";
import { useAppDispatch, useAppSelector } from "@/lib/store";
import { upsertItem, removeItem, selectAllBacklogItems } from "@/lib/store/backlogItemsSlice";
import { mapBacklogItem } from "@/lib/hooks/useBacklogService";
import type { BacklogItem as MappedBacklogItem } from "@/lib/hooks/useBacklogService";

const MAX_RETRIES = 5;
const FALLBACK_POLL_INTERVAL_MS = 30_000;
const BACKSTOP_INTERVAL_MS = 30_000;
const STALE_THRESHOLD_MS = 15_000;
const VISIBILITY_DEBOUNCE_MS = 200;

export interface UseWatchBacklogItemsFilters {
  statusFilter?: string[];
  categoryFilter?: string[];
}

export interface UseWatchBacklogItemsReturn {
  items: MappedBacklogItem[];
  connectionState: BacklogConnectionState;
}

/**
 * Subscribes to real-time backlog item changes via the WatchBacklogItems
 * streaming RPC, dispatching every received event into backlogItemsSlice,
 * and keeps the store fresh across disconnects (exponential-backoff
 * reconnect, REST fallback polling, after_seq replay, and an idle-staleness
 * backstop for periods with zero live events).
 */
export function useWatchBacklogItems(
  filters: UseWatchBacklogItemsFilters = {}
): UseWatchBacklogItemsReturn {
  const { statusFilter, categoryFilter } = filters;
  // Stable string keys so effects don't re-run just because the caller
  // passed a fresh array/object literal this render (a likely pitfall for a
  // hook that Phase 5 will call inline in component bodies).
  const statusFilterKey = (statusFilter ?? []).join(",");
  const categoryFilterKey = (categoryFilter ?? []).join(",");

  const dispatch = useAppDispatch();
  const items = useAppSelector(selectAllBacklogItems);
  const [connectionState, setConnectionState] = useState<BacklogConnectionState>("connecting");

  const clientRef = useRef<ReturnType<typeof createClient<typeof BacklogService>> | null>(null);

  // Stream health/reconnect bookkeeping — hoisted to hook-level refs (rather
  // than effect-local) so the fallback-poll, backstop, and visibility/online
  // effects can all read/drive the same connection state, mirroring
  // useSessionService.ts's ref layout.
  const isConnectedRef = useRef(false);
  const lastEventTimeRef = useRef<number | null>(null);
  const lastSeqRef = useRef<bigint>(0n);
  const resyncInFlightRef = useRef(false);
  // Set right before a backstop- or visibility/online-triggered reconnect;
  // cleared (and a full refetch fired) on that reconnect's first received
  // event. This ties the "reconnect success path issues a refetch"
  // requirement (Story 4.2.3) specifically to self-healing reconnects, not
  // ordinary in-loop backoff retries — and fires even if zero
  // BacklogItemEvents were ever received before the staleness was detected.
  const staleReconnectPendingRef = useRef(false);
  const backstopTriggeredRef = useRef(false);
  const streamRetriesRef = useRef(0);
  const streamDeadRef = useRef(false);
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Set by the main watch effect on every run; called by the fallback-poll,
  // backstop, and visibility/online effects to force a reconnect without
  // needing `connect` in their own dependency arrays.
  const reconnectRef = useRef<(() => void) | null>(null);

  // Initialize ConnectRPC client. Uses plain HTTP (not the WebSocket bridge
  // transport useSessionService/useReviewQueue use for their Watch* RPCs)
  // because BacklogService.WatchBacklogItems is not yet registered with
  // server.go's StreamingWSBridge — standard Connect server-streaming over
  // HTTP works today without that registration; wiring the WS bridge is a
  // separate, larger server.go change out of scope for this frontend epic.
  useEffect(() => {
    const transport = createConnectTransport({
      baseUrl: getApiBaseUrl(),
      interceptors: [createAuthInterceptor()],
    });
    clientRef.current = createClient(BacklogService, transport);
  }, []);

  // Full REST refetch — used for the initial load, gap-detected resyncs, the
  // fallback poll, and every successful (re)connection after the first.
  const refresh = useCallback(async () => {
    if (!clientRef.current) return;
    try {
      const resp = await clientRef.current.listBacklogItems({
        // ListBacklogItemsRequest has no category field (see
        // backlogItemMatchesFilters's doc comment server-side) — only status
        // can be enforced on this REST snapshot; category filtering only
        // applies to the live stream below.
        status: statusFilter ?? [],
        priority: [],
        includeTerminal: false,
        includeArchived: false,
        sortBy: "",
      });
      for (const item of resp.items ?? []) {
        dispatch(upsertItem(item));
      }
    } catch (err) {
      console.error("[useWatchBacklogItems] listBacklogItems failed:", err);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dispatch, statusFilterKey]);

  // On mount, immediately issue the REST snapshot fetch alongside (not
  // gated behind) opening the stream below — Task 4.2.1a/pitfalls.md #1.
  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statusFilterKey]);

  // Gap-detected resync: briefly reflect a "reconnecting" state while the
  // refetch is in flight, then return to "live" if the stream is still up.
  const triggerResync = useCallback(async () => {
    if (resyncInFlightRef.current) return;
    resyncInFlightRef.current = true;
    setConnectionState((prev) => (prev === "live" ? "reconnecting" : prev));
    try {
      await refresh();
    } finally {
      resyncInFlightRef.current = false;
      if (isConnectedRef.current) setConnectionState("live");
    }
  }, [refresh]);

  // Apply after_seq bookkeeping (Story 4.2.2) then dispatch to the store.
  const handleEvent = useCallback(
    (event: BacklogItemEvent) => {
      lastEventTimeRef.current = Date.now();

      // seq === 0 marks a synthetic per-item snapshot event sent on a fresh
      // (non-replay) connection — it carries no real bus sequence number, so
      // it must not participate in gap detection (see BacklogItemEvent.seq's
      // proto doc comment).
      const seq = event.seq;
      if (seq > 0n) {
        const prev = lastSeqRef.current;
        if (prev > 0n) {
          if (seq < prev) {
            // Backwards jump: server restarted and its seq counter reset.
            // Mirrors useSessionService.ts:730-742 exactly.
            lastSeqRef.current = 0n;
            void triggerResync();
          } else if (seq !== prev + 1n) {
            // Forward gap: the bus's bounded, non-blocking fan-out dropped
            // an event under backpressure (pitfalls.md #4 — new logic, not
            // present in useSessionService.ts today).
            lastSeqRef.current = seq;
            void triggerResync();
          } else {
            lastSeqRef.current = seq;
          }
        } else {
          // No established baseline yet (very first real-seq event this
          // client has ever seen) — seed it without treating an arbitrary
          // starting seq as a gap.
          lastSeqRef.current = seq;
        }
      }

      switch (event.event.case) {
        case "statusChanged":
        case "verdictRecorded":
        case "sessionAttached":
        case "itemUpdated": {
          const item = event.event.value.item;
          if (item) dispatch(upsertItem(item));
          break;
        }
        case "itemArchived":
          // Intentionally not applied to backlogItemsSlice — see file header.
          break;
        case "itemRemoved":
          dispatch(removeItem(event.event.value.itemId));
          break;
        default:
          break;
      }
    },
    [dispatch, triggerResync]
  );

  // Main stream connection lifecycle: connect, consume via `for await`,
  // reconnect with exponential backoff on error (capped at 30s, 5 retries,
  // matching useReviewQueue.ts's constants exactly), then fall back to
  // polling once retries are exhausted.
  useEffect(() => {
    streamRetriesRef.current = 0;
    streamDeadRef.current = false;

    const abortController = new AbortController();
    const signal = abortController.signal;

    const connect = async () => {
      if (signal.aborted || !clientRef.current) return;

      // Treat stream (re)connect attempts as activity so the 30s backstop
      // below can engage even if the connection never yields a single event
      // (mirrors useSessionService.ts:822 exactly).
      lastEventTimeRef.current = Date.now();

      try {
        const stream = clientRef.current.watchBacklogItems(
          {
            statusFilter: statusFilter ?? [],
            categoryFilter: categoryFilter ?? [],
            afterSeq: lastSeqRef.current,
          },
          { signal }
        );

        let firstEvent = true;
        for await (const event of stream) {
          if (firstEvent) {
            firstEvent = false;
            isConnectedRef.current = true;
            backstopTriggeredRef.current = false;
            streamRetriesRef.current = 0;
            streamDeadRef.current = false;
            setConnectionState("live");
            // Story 4.2.3: a backstop- or visibility-triggered reconnect's
            // success path issues a full refetch, even if zero
            // BacklogItemEvents were ever received during the whole idle
            // period beforehand.
            if (staleReconnectPendingRef.current) {
              staleReconnectPendingRef.current = false;
              void refresh();
            }
          }
          handleEvent(event);
        }

        // Clean server-side close — reset retry counter; the fallback-poll/
        // backstop/visibility effects will drive the next reconnect attempt.
        isConnectedRef.current = false;
        streamRetriesRef.current = 0;
      } catch (err) {
        isConnectedRef.current = false;
        if (err instanceof Error && err.name === "AbortError") return;
        if (signal.aborted) return;

        console.error("[useWatchBacklogItems] watchBacklogItems stream error:", err);

        if (streamRetriesRef.current < MAX_RETRIES) {
          const delay = Math.min(1000 * Math.pow(2, streamRetriesRef.current), 30_000);
          streamRetriesRef.current++;
          setConnectionState("reconnecting");
          setTimeout(() => {
            if (signal.aborted) return;
            void connect();
          }, delay);
        } else {
          streamDeadRef.current = true;
          setConnectionState("polling");
        }
      }
    };

    reconnectRef.current = () => void connect();
    void connect();

    return () => {
      reconnectRef.current = null;
      abortController.abort();
      isConnectedRef.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statusFilterKey, categoryFilterKey, handleEvent, refresh]);

  // REST fallback polling (Task 4.2.1d): once retries are exhausted, poll
  // periodically; a successful poll that finds the stream dead attempts
  // exactly one reconnect before continuing to poll.
  useEffect(() => {
    const interval = setInterval(() => {
      void (async () => {
        await refresh();
        if (streamDeadRef.current) {
          streamDeadRef.current = false;
          streamRetriesRef.current = 0;
          reconnectRef.current?.();
        }
      })();
    }, FALLBACK_POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [refresh]);

  // Story 4.2.3 — idle-staleness backstop #1: a 30s periodic timer that
  // forces a reconnect + full refetch even with zero live events, mirroring
  // useSessionService.ts:944-962 verbatim.
  useEffect(() => {
    const interval = setInterval(() => {
      if (
        !isConnectedRef.current &&
        lastEventTimeRef.current !== null &&
        Date.now() - lastEventTimeRef.current > BACKSTOP_INTERVAL_MS
      ) {
        setConnectionState("stale");
        if (!backstopTriggeredRef.current) {
          backstopTriggeredRef.current = true;
          staleReconnectPendingRef.current = true;
          reconnectRef.current?.();
        }
      }
    }, BACKSTOP_INTERVAL_MS);
    return () => clearInterval(interval);
  }, []);

  // Story 4.2.3 — idle-staleness backstop #2: a 15s staleness threshold on
  // visibility/online events, mirroring useSessionService.ts:971-986.
  useEffect(() => {
    const handleVisibilityOrOnline = (ev: Event) => {
      if (document.visibilityState !== "visible" && ev.type !== "online") return;
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
      debounceTimerRef.current = setTimeout(() => {
        debounceTimerRef.current = null;
        const isStale =
          lastEventTimeRef.current !== null && lastEventTimeRef.current < Date.now() - STALE_THRESHOLD_MS;
        if (!isConnectedRef.current || isStale) {
          if (isStale) setConnectionState("stale");
          staleReconnectPendingRef.current = true;
          streamRetriesRef.current = 0;
          streamDeadRef.current = false;
          reconnectRef.current?.();
        }
      }, VISIBILITY_DEBOUNCE_MS);
    };

    document.addEventListener("visibilitychange", handleVisibilityOrOnline);
    window.addEventListener("online", handleVisibilityOrOnline);
    return () => {
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
      document.removeEventListener("visibilitychange", handleVisibilityOrOnline);
      window.removeEventListener("online", handleVisibilityOrOnline);
    };
  }, []);

  // Map proto -> domain shape at the hook boundary (see file header, note 3)
  // so every consumer gets the same fields useBacklogService.listBacklogItems
  // already produces, rather than each render component reimplementing
  // (and risking drift from) mapBacklogItem's derivation logic.
  const mappedItems = useMemo(() => items.map(mapBacklogItem), [items]);

  return useMemo(() => ({ items: mappedItems, connectionState }), [mappedItems, connectionState]);
}
