/**
 * Tests for useWatchBacklogItems — Epic 4.2 (backlog-event-driven-updates).
 *
 * Covers: initial REST+stream sequencing, exponential-backoff reconnect,
 * REST fallback polling, after_seq tracking + forward/backward gap
 * detection (Story 4.2.2), and the idle-staleness backstop (Story 4.2.3 —
 * pre-mortem.md P2 #1's explicit requirement: a fake-timer test asserting a
 * refetch fires after simulated silence past the 30s/15s thresholds).
 */

import { renderHook, act } from "@testing-library/react";
import React from "react";
import { Provider } from "react-redux";
import { configureStore } from "@reduxjs/toolkit";
import backlogItemsReducer, { selectBacklogItemById, selectAllBacklogItems } from "@/lib/store/backlogItemsSlice";

// ── Mocks ──────────────────────────────────────────────────────────────────

const mockListBacklogItems = jest.fn();
const mockWatchBacklogItems = jest.fn();

jest.mock("@connectrpc/connect", () => ({
  createClient: () => ({
    listBacklogItems: mockListBacklogItems,
    watchBacklogItems: mockWatchBacklogItems,
  }),
}));

jest.mock("@connectrpc/connect-web", () => ({
  createConnectTransport: jest.fn().mockReturnValue({}),
}));

jest.mock("@/lib/config", () => ({
  getApiBaseUrl: () => "http://localhost:8543",
  createAuthInterceptor: () => jest.fn(),
}));

import { useWatchBacklogItems } from "../useWatchBacklogItems";

// ── Store factory ──────────────────────────────────────────────────────────

function makeStore() {
  return configureStore({
    reducer: { backlogItems: backlogItemsReducer },
    middleware: (getDefault) => getDefault({ serializableCheck: false }),
  });
}

function makeWrapper(store: ReturnType<typeof makeStore>) {
  function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(Provider, { store } as any, children);
  }
  return Wrapper;
}

function makeItem(id: string, status = "in_progress") {
  return { id, status } as any;
}

function makeEvent(caseName: string, value: unknown, seq: bigint) {
  return { seq, event: { case: caseName, value } } as any;
}

/** A hanging async iterable — never yields, never throws. Simulates total silence. */
function makeHangingStream() {
  return { [Symbol.asyncIterator]: () => ({ next: () => new Promise(() => {}) }) };
}

/** Async-iterable test double with a manually-controlled event queue. */
function makeControllableStream() {
  type QueueItem = { kind: "event"; value: unknown } | { kind: "error"; error: unknown } | { kind: "done" };
  const queue: QueueItem[] = [];
  let notify: (() => void) | null = null;

  const push = (item: QueueItem) => {
    queue.push(item);
    const n = notify;
    notify = null;
    n?.();
  };

  const stream = {
    [Symbol.asyncIterator]: () => ({
      next: async () => {
        while (queue.length === 0) {
          await new Promise<void>((r) => {
            notify = r;
          });
        }
        const item = queue.shift()!;
        if (item.kind === "done") return { done: true, value: undefined };
        if (item.kind === "error") throw item.error;
        return { done: false, value: item.value };
      },
    }),
  };

  return {
    stream,
    emit: (value: unknown) => push({ kind: "event", value }),
    fail: (error: unknown) => push({ kind: "error", error }),
    end: () => push({ kind: "done" }),
  };
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
}

describe("useWatchBacklogItems", () => {
  beforeEach(() => {
    mockListBacklogItems.mockReset();
    mockWatchBacklogItems.mockReset();
    mockListBacklogItems.mockResolvedValue({ items: [] });
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  // R11 happy — Task 4.2.1a/b
  it("fires a listBacklogItems REST call on mount alongside opening the stream", async () => {
    mockWatchBacklogItems.mockReturnValue(makeHangingStream());
    const store = makeStore();

    renderHook(() => useWatchBacklogItems({ statusFilter: ["in_progress"] }), {
      wrapper: makeWrapper(store),
    });

    await act(async () => {
      await flush();
    });

    expect(mockListBacklogItems).toHaveBeenCalledTimes(1);
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(1);
  });

  // R11 error path — Task 4.2.1c
  it("retries with exponential backoff capped at 30s on stream error", async () => {
    jest.useFakeTimers();
    mockWatchBacklogItems.mockImplementation(() => {
      throw new Error("stream failure");
    });

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(1);

    // Delays: attempt 0 -> 1000ms, attempt 1 -> 2000ms, attempt 2 -> 4000ms.
    // After these three delays elapse, the 4th connect() call has happened
    // and thrown, scheduling attempt 3's retry at exactly 8000ms.
    for (const ms of [1000, 2000, 4000]) {
      await act(async () => {
        jest.advanceTimersByTime(ms);
        await flush();
      });
    }
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(4);

    // Confirm attempt 3's delay is exactly min(1000*2^3, 30000) = 8000ms:
    // advancing just short of it must NOT yet trigger the 5th call.
    await act(async () => {
      jest.advanceTimersByTime(7999);
      await flush();
    });
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(4);

    await act(async () => {
      jest.advanceTimersByTime(1);
      await flush();
    });
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(5);
  });

  // R11 integration — Task 4.2.1d
  it("falls back to REST polling after retries exhaust and attempts one reconnect on next successful poll", async () => {
    jest.useFakeTimers();
    mockWatchBacklogItems.mockImplementation(() => {
      throw new Error("stream failure");
    });

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });

    // Exhaust MAX_RETRIES (5): delays 1000+2000+4000+8000+16000 = 31000ms.
    // Note: this cumulative window (31s) overlaps the 30s fallback-poll
    // interval, so — exactly as useReviewQueue.test.ts's equivalent test
    // documents — the poll may ALSO fire and attempt its own reconnect
    // during this sequence. Assert a floor, not an exact count.
    for (const ms of [1000, 2000, 4000, 8000, 16000]) {
      await act(async () => {
        jest.advanceTimersByTime(ms);
        await flush();
      });
    }
    // At minimum: 1 initial + 5 retries = 6 calls.
    expect(mockWatchBacklogItems.mock.calls.length).toBeGreaterThanOrEqual(6);
    mockWatchBacklogItems.mockClear();
    mockListBacklogItems.mockClear();
    // The server has "recovered" — the next reconnect attempt (triggered by
    // the poll below) succeeds instead of immediately re-throwing, so we can
    // isolate "exactly one reconnect attempt" without a cascade of further
    // backoff retries within the same fake-timer advance.
    mockWatchBacklogItems.mockImplementationOnce(() => makeHangingStream());

    // Fallback poll (30s interval) ticks: successful REST call while the
    // stream is dead triggers exactly one reconnect attempt.
    await act(async () => {
      jest.advanceTimersByTime(30_000);
      await flush();
    });
    expect(mockListBacklogItems.mock.calls.length).toBeGreaterThanOrEqual(1);
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(1);
  });

  // R12 happy — Task 4.2.2a
  it("passes lastSeq as after_seq on reconnect", async () => {
    jest.useFakeTimers();
    const first = makeControllableStream();
    mockWatchBacklogItems.mockReturnValueOnce(first.stream);
    mockWatchBacklogItems.mockReturnValueOnce(makeHangingStream());

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    expect(mockWatchBacklogItems).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ afterSeq: 0n }),
      expect.anything()
    );

    await act(async () => {
      first.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 517n));
      await flush();
    });

    await act(async () => {
      first.fail(new Error("disconnect"));
      await flush();
    });
    // First retry delay: min(1000*2^0, 30000) = 1000ms.
    await act(async () => {
      jest.advanceTimersByTime(1000);
      await flush();
    });

    expect(mockWatchBacklogItems).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ afterSeq: 517n }),
      expect.anything()
    );
  });

  // R12 error path — Task 4.2.2b
  it("triggers full resync when a fresh connection's first seq is behind lastSeqRef (server restart)", async () => {
    jest.useFakeTimers();
    const first = makeControllableStream();
    const second = makeControllableStream();
    mockWatchBacklogItems.mockReturnValueOnce(first.stream);
    mockWatchBacklogItems.mockReturnValueOnce(second.stream);
    mockWatchBacklogItems.mockReturnValue(makeHangingStream());

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    mockListBacklogItems.mockClear();

    // Establish a clean baseline of 800 (no gaps): 799, then 800.
    await act(async () => {
      first.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 799n));
      await flush();
    });
    await act(async () => {
      first.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 800n));
      await flush();
    });

    await act(async () => {
      first.fail(new Error("disconnect"));
      await flush();
    });
    await act(async () => {
      jest.advanceTimersByTime(1000);
      await flush();
    });

    // Reconnect's first event has a much smaller seq — server restarted.
    await act(async () => {
      second.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 12n));
      await flush();
    });

    expect(mockListBacklogItems).toHaveBeenCalledTimes(1);
  });

  // R12 integration — Task 4.2.2c/d
  it("triggers full resync and advances lastSeqRef when a forward gap is detected", async () => {
    jest.useFakeTimers();
    const stream = makeControllableStream();
    mockWatchBacklogItems.mockReturnValueOnce(stream.stream);
    mockWatchBacklogItems.mockReturnValue(makeHangingStream());

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    mockListBacklogItems.mockClear();

    // Establish baseline 100 cleanly (99, then 100).
    await act(async () => {
      stream.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 99n));
      await flush();
    });
    await act(async () => {
      stream.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 100n));
      await flush();
    });

    // Gap: 101-102 dropped, next live event is 103.
    await act(async () => {
      stream.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 103n));
      await flush();
    });

    expect(mockListBacklogItems).toHaveBeenCalledTimes(1);

    // lastSeqRef advanced to 103 — verify indirectly via the next reconnect's afterSeq.
    await act(async () => {
      stream.fail(new Error("disconnect"));
      await flush();
    });
    await act(async () => {
      jest.advanceTimersByTime(1000);
      await flush();
    });
    expect(mockWatchBacklogItems).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ afterSeq: 103n }),
      expect.anything()
    );
  });

  // item_archived design resolution (Q1): no slice dispatch for this variant.
  it("does not apply an item_archived event to backlogItemsSlice", async () => {
    const stream = makeControllableStream();
    mockWatchBacklogItems.mockReturnValueOnce(stream.stream);
    mockWatchBacklogItems.mockReturnValue(makeHangingStream());
    mockListBacklogItems.mockResolvedValue({ items: [makeItem("item-1")] });

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    expect(selectBacklogItemById(store.getState() as any, "item-1")).toBeDefined();

    await act(async () => {
      stream.emit(makeEvent("itemArchived", { itemId: "item-1", isSnapshot: false }, 1n));
      await flush();
    });

    // Item is untouched by the slice (still present, not removed) — Phase 5
    // components handle archived-state UI via a separate mechanism.
    expect(selectBacklogItemById(store.getState() as any, "item-1")).toBeDefined();
    expect(selectAllBacklogItems(store.getState() as any)).toHaveLength(1);
  });

  // Story 4.2.3 — pre-mortem.md P2 #1's explicit requirement.
  it("30s idle backstop forces a reconnect and a full refetch after simulated silence past the timeout", async () => {
    jest.useFakeTimers();

    const hanging = makeHangingStream();
    const second = makeControllableStream();
    mockWatchBacklogItems.mockReturnValueOnce(hanging);
    mockWatchBacklogItems.mockReturnValueOnce(second.stream);
    mockWatchBacklogItems.mockReturnValue(makeHangingStream());

    const store = makeStore();
    const { result } = renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(1);
    mockListBacklogItems.mockClear();

    // The connection never yields a single event; advance well past the 30s
    // backstop threshold (two interval ticks to comfortably clear the
    // strict "> 30000ms" boundary).
    await act(async () => {
      jest.advanceTimersByTime(60_000);
      await flush();
    });

    // Exactly one reconnect attempt triggered by the backstop.
    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(2);
    expect(result.current.connectionState).toBe("stale");

    // The 30s fallback-poll interval also ticks (unconditionally) during
    // this 60s window and independently calls listBacklogItems — clear it
    // so the next assertion isolates the reconnect-success refetch alone.
    mockListBacklogItems.mockClear();

    // That reconnect's success path (first event received) issues a full refetch.
    await act(async () => {
      second.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 5n));
      await flush();
    });
    expect(mockListBacklogItems).toHaveBeenCalledTimes(1);
    expect(result.current.connectionState).toBe("live");
  });

  // Story 4.2.3 — 15s visibility/online staleness path.
  it("15s visibility staleness check forces a reconnect when the tab regains focus after prolonged silence", async () => {
    jest.useFakeTimers();

    const first = makeControllableStream();
    const second = makeControllableStream();
    mockWatchBacklogItems.mockReturnValueOnce(first.stream);
    mockWatchBacklogItems.mockReturnValueOnce(second.stream);
    mockWatchBacklogItems.mockReturnValue(makeHangingStream());

    const store = makeStore();
    renderHook(() => useWatchBacklogItems(), { wrapper: makeWrapper(store) });

    await act(async () => {
      await flush();
    });
    // Receive one live event so the stream is marked connected/live, then
    // go quiet (no more events) for over 15s while "backgrounded".
    await act(async () => {
      first.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 1n));
      await flush();
    });
    mockListBacklogItems.mockClear();

    await act(async () => {
      jest.advanceTimersByTime(20_000);
    });

    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      jest.advanceTimersByTime(200); // debounce
      await flush();
    });

    expect(mockWatchBacklogItems).toHaveBeenCalledTimes(2);

    await act(async () => {
      second.emit(makeEvent("itemUpdated", { item: makeItem("item-1"), itemId: "item-1", updatedFields: [] }, 2n));
      await flush();
    });
    expect(mockListBacklogItems).toHaveBeenCalledTimes(1);
  });
});
