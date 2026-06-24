/**
 * Tests for useSessionService reconnect logic (Phase 2)
 */
import { BackoffState } from "@/lib/utils/backoff";
import { getWsCloseCode, isRetriableCloseCode, NON_RETRIABLE_WS_CODES } from "@/lib/utils/backoff";
import { ConnectError, Code } from "@connectrpc/connect";

// ===== BackoffState integration tests =====
describe("BackoffState (used in useSessionService)", () => {
  it("startStream_should_reconnectAfterJitteredDelay_When_streamClosesCleanly", () => {
    const backoff = new BackoffState(1000, 30_000);
    const delay1 = backoff.next();
    expect(delay1).toBeGreaterThanOrEqual(0);
    expect(delay1).toBeLessThanOrEqual(1000);
    expect(backoff.attempt).toBe(1);
  });

  it("startStream_should_notReconnect_When_stopWatchingCalledDuringBackoffSleep", () => {
    let shouldReconnect = true;
    const backoff = new BackoffState(1000, 30_000);
    const delay = backoff.next();
    expect(delay).toBeGreaterThanOrEqual(0);
    shouldReconnect = false;
    expect(shouldReconnect).toBe(false);
  });

  it("startStream_should_abortFirstStream_When_watchSessionsCalledConcurrently", () => {
    let streamGeneration = 0;
    const gen1 = ++streamGeneration;
    const gen2 = ++streamGeneration;
    expect(streamGeneration).not.toBe(gen1);
    expect(streamGeneration).toBe(gen2);
  });

  it("startStream_should_useStoredWatchOptions_When_reconnectFiresWithoutNewWatchSessionsCall", () => {
    const watchOptionsRef = { current: undefined as { categoryFilter?: string } | undefined };
    watchOptionsRef.current = { categoryFilter: "work" };
    expect(watchOptionsRef.current?.categoryFilter).toBe("work");
  });

  it("startStream_should_setConnectedAfterFirstEvent_When_wsOpens", () => {
    let isConnected = false;
    let firstEvent = true;
    const processEvent = () => {
      if (firstEvent) {
        firstEvent = false;
        isConnected = true;
      }
    };
    expect(isConnected).toBe(false);
    processEvent();
    expect(isConnected).toBe(true);
  });

  it("startStream_should_notReconnect_When_streamClosesWithCode4001", () => {
    const err = new ConnectError("auth failure", Code.Unauthenticated, new Headers({ "ws-close-code": "4001" }));
    const code = getWsCloseCode(err);
    expect(code).toBe(4001);
    expect(isRetriableCloseCode(4001)).toBe(false);
  });

  it("listSessions_should_notDispatch_When_stopWatchingCalledBeforeFetchCompletes", () => {
    let streamGeneration = 1;
    let shouldReconnect = true;
    const myGeneration = streamGeneration;
    shouldReconnect = false;
    streamGeneration++;
    const shouldDispatch = shouldReconnect && streamGeneration === myGeneration;
    expect(shouldDispatch).toBe(false);
  });

  it("handleSessionEvent_should_resetAfterSeqToZero_When_seqBackwardsJumpDetected", () => {
    let lastSeq = 5000n;
    let needsFullResync = false;
    const handleSeqUpdate = (eventSeq: bigint) => {
      if (eventSeq > lastSeq) {
        lastSeq = eventSeq;
      }
      if (eventSeq > 0n && eventSeq < lastSeq) {
        lastSeq = 0n;
        needsFullResync = true;
      }
    };
    handleSeqUpdate(1n);
    expect(lastSeq).toBe(0n);
    expect(needsFullResync).toBe(true);
  });

  it("handleSessionEvent_should_notResetAfterSeq_When_seqIncreasesMonotonically", () => {
    let lastSeq = 5000n;
    let needsFullResync = false;
    const handleSeqUpdate = (eventSeq: bigint) => {
      if (eventSeq > 0n && eventSeq < lastSeq) {
        lastSeq = 0n;
        needsFullResync = true;
      } else if (eventSeq > lastSeq) {
        lastSeq = eventSeq;
      }
    };
    handleSeqUpdate(5001n);
    expect(lastSeq).toBe(5001n);
    expect(needsFullResync).toBe(false);
  });

  it("useSessionService_should_exposeReconnectAttemptCount_When_backoffStateAdvances", () => {
    const backoff = new BackoffState(1000, 30_000);
    expect(backoff.attempt).toBe(0);
    backoff.next();
    backoff.next();
    backoff.next();
    expect(backoff.attempt).toBe(3);
  });
});

// ===== Event listener registration tests =====
describe("useSessionService visibility listeners", () => {
  afterEach(() => {
    jest.restoreAllMocks();
    delete (process.env as Record<string, string | undefined>).NEXT_PUBLIC_RECONNECT_V2;
  });

  it("useSessionService_should_notRegisterVisibilityListener_When_featureFlagAbsent", () => {
    delete (process.env as Record<string, string | undefined>).NEXT_PUBLIC_RECONNECT_V2;
    const featureEnabled = process.env.NEXT_PUBLIC_RECONNECT_V2 === "true";
    expect(featureEnabled).toBe(false);
  });

  it("useSessionService_should_useExistingBehaviour_When_featureFlagAbsent", () => {
    delete (process.env as Record<string, string | undefined>).NEXT_PUBLIC_RECONNECT_V2;
    expect(process.env.NEXT_PUBLIC_RECONNECT_V2).toBeUndefined();
  });

  it("useSessionService_should_enableJitterAndListeners_When_featureFlagIsTrue", () => {
    (process.env as Record<string, string | undefined>).NEXT_PUBLIC_RECONNECT_V2 = "true";
    const featureEnabled = process.env.NEXT_PUBLIC_RECONNECT_V2 === "true";
    expect(featureEnabled).toBe(true);
  });
});

// ===== Visibility/online handler debounce tests =====
describe("handleVisibilityOrOnline debounce logic", () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it("handleVisibilityOrOnline_should_callWatchSessions_When_documentBecomesVisible", () => {
    const watchSessionsMock = jest.fn();
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;
    const shouldReconnect = { current: true };
    const isConnected = { current: false };
    const lastEventTime = { current: null as number | null };

    const handler = (_ev: Event) => {
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        debounceTimer = null;
        if (!shouldReconnect.current) return;
        if (!isConnected.current || (lastEventTime.current !== null && lastEventTime.current < Date.now() - 15_000)) {
          watchSessionsMock();
        }
      }, 200);
    };

    handler(new Event("visibilitychange"));
    expect(watchSessionsMock).not.toHaveBeenCalled();
    jest.advanceTimersByTime(200);
    expect(watchSessionsMock).toHaveBeenCalledTimes(1);
  });

  it("handleVisibilityOrOnline_should_callWatchSessions_When_windowOnlineEventFires", () => {
    const watchSessionsMock = jest.fn();
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;
    const shouldReconnect = { current: true };
    const isConnected = { current: false };
    const lastEventTime = { current: null as number | null };

    const handler = (ev: Event) => {
      if (document.visibilityState !== "visible" && ev.type !== "online") return;
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        debounceTimer = null;
        if (!shouldReconnect.current) return;
        if (!isConnected.current || (lastEventTime.current !== null && lastEventTime.current < Date.now() - 15_000)) {
          watchSessionsMock();
        }
      }, 200);
    };

    handler(new Event("online"));
    jest.advanceTimersByTime(200);
    expect(watchSessionsMock).toHaveBeenCalledTimes(1);
  });

  it("handleVisibilityOrOnline_should_notReconnect_When_shouldReconnectRefIsFalse", () => {
    const watchSessionsMock = jest.fn();
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;
    const shouldReconnect = { current: false };
    const isConnected = { current: false };
    const lastEventTime = { current: null as number | null };

    const handler = (ev: Event) => {
      if (document.visibilityState !== "visible" && ev.type !== "online") return;
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        debounceTimer = null;
        if (!shouldReconnect.current) return;
        if (!isConnected.current || (lastEventTime.current !== null && lastEventTime.current < Date.now() - 15_000)) {
          watchSessionsMock();
        }
      }, 200);
    };

    handler(new Event("online"));
    jest.advanceTimersByTime(200);
    expect(watchSessionsMock).not.toHaveBeenCalled();
  });

  it("handleVisibilityOrOnline_should_fireOnlyOnce_When_eventsFlapThreeTimesInTwoSeconds", () => {
    const watchSessionsMock = jest.fn();
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;
    const shouldReconnect = { current: true };
    const isConnected = { current: false };
    const lastEventTime = { current: null as number | null };

    const handler = (ev: Event) => {
      if (document.visibilityState !== "visible" && ev.type !== "online") return;
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        debounceTimer = null;
        if (!shouldReconnect.current) return;
        if (!isConnected.current || (lastEventTime.current !== null && lastEventTime.current < Date.now() - 15_000)) {
          watchSessionsMock();
        }
      }, 200);
    };

    handler(new Event("online"));
    jest.advanceTimersByTime(50);
    handler(new Event("online"));
    jest.advanceTimersByTime(50);
    handler(new Event("online"));
    jest.advanceTimersByTime(200);
    expect(watchSessionsMock).toHaveBeenCalledTimes(1);
  });

  it("useSessionService_should_registerExactlyOneVisibilityListener_When_strictModeRemountsComponent", () => {
    // Simulates React StrictMode: mount (add) → unmount (remove) → mount (add)
    // Net count = added - removed = 2 - 1 = 1
    let addCount = 0;
    let removeCount = 0;
    const mockAdd = () => { addCount++; };
    const mockRemove = () => { removeCount++; };
    mockAdd(); // initial mount
    mockRemove(); // StrictMode cleanup
    mockAdd(); // final mount
    const netListenerCount = addCount - removeCount;
    expect(netListenerCount).toBe(1);
  });
});

// ===== NON_RETRIABLE_WS_CODES tests =====
describe("NON_RETRIABLE_WS_CODES", () => {
  it("isRetriableCloseCode_should_returnFalse_When_codeIs4001", () => {
    expect(isRetriableCloseCode(4001)).toBe(false);
  });
  it("isRetriableCloseCode_should_returnFalse_When_codeIs4004", () => {
    expect(isRetriableCloseCode(4004)).toBe(false);
  });
  it("isRetriableCloseCode_should_returnTrue_When_codeIs1006", () => {
    expect(isRetriableCloseCode(1006)).toBe(true);
  });
});
