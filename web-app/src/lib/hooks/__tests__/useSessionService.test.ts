import { renderHook, act } from "@testing-library/react";
import { useSessionService } from "../useSessionService";
import { createClient } from "@connectrpc/connect";
import { useAppDispatch, useAppSelector } from "@/lib/store";
import {
  setSessions,
  upsertSession,
  removeSession,
  setLoading,
  setError,
  setConnectionState,
  updateSessionStatus,
} from "@/lib/store/sessionsSlice";
import { SessionStatus } from "@/gen/session/v1/types_pb";

// ── Mocks ──────────────────────────────────────────────────────────────────

jest.mock("@connectrpc/connect", () => ({
  createClient: jest.fn(),
}));

jest.mock("@/lib/transport/watch-ws-transport", () => ({
  createWatchTransport: jest.fn(),
}));

jest.mock("@/lib/config", () => ({
  getApiBaseUrl: () => "http://localhost:8543",
  createAuthInterceptor: jest.fn(),
}));

jest.mock("@/lib/telemetry/rpcTiming", () => ({
  createRpcTimingInterceptor: jest.fn(),
}));

jest.mock("@/lib/contexts/AnalyticsContext", () => ({
  useAnalytics: () => ({}),
}));

jest.mock("@/lib/store", () => ({
  useAppDispatch: jest.fn(),
  useAppSelector: jest.fn(),
}));

// Mock selectors by their name if possible, or just mock the values they return
jest.mock("@/lib/store/sessionsSlice", () => {
  return {
    setSessions: jest.fn((p) => ({ type: "sessions/setSessions", payload: p })),
    upsertSession: jest.fn((p) => ({ type: "sessions/upsertSession", payload: p })),
    removeSession: jest.fn((p) => ({ type: "sessions/removeSession", payload: p })),
    setLoading: jest.fn((p) => ({ type: "sessions/setLoading", payload: p })),
    setError: jest.fn((p) => ({ type: "sessions/setError", payload: p })),
    setConnectionState: jest.fn((p) => ({ type: "sessions/setConnectionState", payload: p })),
    updateSessionStatus: jest.fn((p) => ({ type: "sessions/updateSessionStatus", payload: p })),
    selectAllSessions: jest.fn(),
    selectSessionsLoading: jest.fn(),
    selectSessionsError: jest.fn(),
    selectConnectionState: jest.fn(),
  };
});

// Mock SessionService for createClient
jest.mock("@/gen/session/v1/session_pb", () => ({
  SessionService: {
    typeName: "session.v1.SessionService",
    methods: {
      listSessions: { name: "ListSessions", kind: "unary" },
      watchSessions: { name: "WatchSessions", kind: "server_streaming" },
    }
  },
}));

// ── Test Setup ─────────────────────────────────────────────────────────────

describe("useSessionService", () => {
  let mockDispatch: jest.Mock;
  let mockClient: any;

  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers();

    mockDispatch = jest.fn();
    (useAppDispatch as jest.Mock).mockReturnValue(mockDispatch);
    (useAppSelector as jest.Mock).mockImplementation((selector) => {
      // selectors are passed directly to useAppSelector
      // In useSessionService:
      // sessions = useAppSelector(selectAllSessions)
      // loading = useAppSelector(selectSessionsLoading)
      // errorStr = useAppSelector(selectSessionsError)
      // connectionState = useAppSelector(selectConnectionState)

      const {
        selectAllSessions,
        selectSessionsLoading,
        selectSessionsError,
        selectConnectionState
      } = jest.requireMock("@/lib/store/sessionsSlice");

      if (selector === selectAllSessions) return [];
      if (selector === selectSessionsLoading) return false;
      if (selector === selectSessionsError) return null;
      if (selector === selectConnectionState) return "disconnected";
      return undefined;
    });

    mockClient = {
      listSessions: jest.fn().mockResolvedValue({ sessions: [] }),
      getSession: jest.fn(),
      createSession: jest.fn(),
      updateSession: jest.fn(),
      deleteSession: jest.fn(),
      renameSession: jest.fn(),
      restartSession: jest.fn(),
      clearConversationState: jest.fn(),
      acknowledgeSession: jest.fn(),
      createCheckpoint: jest.fn(),
      listCheckpoints: jest.fn(),
      forkSession: jest.fn(),
      runOneShot: jest.fn(),
      listPromptHistory: jest.fn(),
      watchSessions: jest.fn(),
    };

    (createClient as jest.Mock).mockReturnValue(mockClient);
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  // ── Unary RPCs ───────────────────────────────────────────────────────────

  describe("listSessions", () => {
    it("calls listSessions and dispatches results", async () => {
      const mockSessions = [{ id: "s1", title: "Session 1" }];
      mockClient.listSessions.mockResolvedValue({ sessions: mockSessions });

      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        await result.current.listSessions();
      });

      expect(mockClient.listSessions).toHaveBeenCalled();
      expect(mockDispatch).toHaveBeenCalledWith(setLoading(true));
      expect(mockDispatch).toHaveBeenCalledWith(setSessions(mockSessions as any));
      expect(mockDispatch).toHaveBeenCalledWith(setLoading(false));
    });

    it("handles errors during listSessions", async () => {
      const error = new Error("Network error");
      mockClient.listSessions.mockRejectedValue(error);

      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        await result.current.listSessions();
      });

      expect(mockDispatch).toHaveBeenCalledWith(setError(error.message));
    });
  });

  describe("createSession", () => {
    it("calls createSession and dispatches upsertSession", async () => {
      const newSession = { id: "s2", title: "New Session" };
      mockClient.createSession.mockResolvedValue({ session: newSession });

      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        const created = await result.current.createSession({ title: "New Session" });
        expect(created).toEqual(newSession);
      });

      expect(mockClient.createSession).toHaveBeenCalledWith(expect.objectContaining({ title: "New Session" }));
      expect(mockDispatch).toHaveBeenCalledWith(upsertSession(newSession as any));
    });

    it("throws and sets error if createSession fails", async () => {
      const error = new Error("Create failed");
      mockClient.createSession.mockRejectedValue(error);

      const { result } = renderHook(() => useSessionService());

      await expect(
        act(async () => {
          await result.current.createSession({ title: "Fail" });
        })
      ).rejects.toThrow("Create failed");

      expect(mockDispatch).toHaveBeenCalledWith(setError(error.message));
    });
  });

  describe("updateSession", () => {
    it("calls updateSession and dispatches upsertSession", async () => {
      const updatedSession = { id: "s1", title: "Updated" };
      mockClient.updateSession.mockResolvedValue({ session: updatedSession });

      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        const res = await result.current.updateSession("s1", { title: "Updated" });
        expect(res).toEqual(updatedSession);
      });

      expect(mockClient.updateSession).toHaveBeenCalledWith(expect.objectContaining({ id: "s1", title: "Updated" }));
      expect(mockDispatch).toHaveBeenCalledWith(upsertSession(updatedSession as any));
    });
  });

  describe("deleteSession", () => {
    it("calls deleteSession and dispatches removeSession on success", async () => {
      mockClient.deleteSession.mockResolvedValue({ success: true });

      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        const success = await result.current.deleteSession("s1");
        expect(success).toBe(true);
      });

      expect(mockClient.deleteSession).toHaveBeenCalledWith({ id: "s1", force: false });
      expect(mockDispatch).toHaveBeenCalledWith(removeSession("s1"));
    });
  });

  // ── Helper methods ───────────────────────────────────────────────────────

  describe("pause/resume", () => {
    it("pauseSession calls updateSession with PAUSED status", async () => {
      mockClient.updateSession.mockResolvedValue({ session: { id: "s1", status: SessionStatus.PAUSED } });
      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        await result.current.pauseSession("s1");
      });

      expect(mockClient.updateSession).toHaveBeenCalledWith(expect.objectContaining({ id: "s1", status: SessionStatus.PAUSED }));
    });

    it("resumeSession calls updateSession with RUNNING status", async () => {
      mockClient.updateSession.mockResolvedValue({ session: { id: "s1", status: SessionStatus.RUNNING } });
      const { result } = renderHook(() => useSessionService());

      await act(async () => {
        await result.current.resumeSession("s1", { title: "Resume" });
      });

      expect(mockClient.updateSession).toHaveBeenCalledWith(expect.objectContaining({
        id: "s1",
        status: SessionStatus.RUNNING,
        title: "Resume"
      }));
    });
  });

  // ── Streaming ────────────────────────────────────────────────────────────

  describe("watchSessions", () => {
    it("starts watching sessions and handles various event types", async () => {
      const mockCreatedEvent = {
        event: {
          case: "sessionCreated",
          value: { session: { id: "s1", title: "Created" } },
        },
      };
      const mockDeletedEvent = {
        event: {
          case: "sessionDeleted",
          value: { sessionId: "s2" },
        },
      };

      async function* mockStream() {
        yield mockCreatedEvent;
        yield mockDeletedEvent;
        await new Promise(() => {}); // hang
      }
      mockClient.watchSessions.mockReturnValue(mockStream());

      const { result } = renderHook(() => useSessionService());

      act(() => {
        result.current.watchSessions();
      });

      // Need multiple act/resolve to process the stream loop
      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(mockDispatch).toHaveBeenCalledWith(setConnectionState("connected"));
      expect(mockDispatch).toHaveBeenCalledWith(upsertSession(mockCreatedEvent.event.value.session as any));
      expect(mockDispatch).toHaveBeenCalledWith(removeSession("s2"));
    });

    it("handles notification events via callback", async () => {
      const onNotification = jest.fn();
      const mockNotifEvent = {
        event: {
          case: "notification",
          value: { message: "Hello" },
        },
      };

      async function* mockStream() {
        yield mockNotifEvent;
        await new Promise(() => {});
      }
      mockClient.watchSessions.mockReturnValue(mockStream());

      const { result } = renderHook(() => useSessionService({ onNotification }));

      act(() => {
        result.current.watchSessions();
      });

      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });

      expect(onNotification).toHaveBeenCalledWith(mockNotifEvent.event.value);
    });

    it("reconnects on stream end with backoff", async () => {
      const onReconnect = jest.fn();
      let streamCount = 0;
      mockClient.watchSessions.mockImplementation(() => {
        streamCount++;
        return (async function* () {
          yield { event: { case: "sessionUpdated", value: { session: { id: "s1" } } } };
          // Stream ends
        })();
      });

      const { result } = renderHook(() => useSessionService({ onReconnect }));

      await act(async () => {
        result.current.watchSessions();
      });

      // After first stream ends
      await act(async () => {
        await Promise.resolve(); // handle event
        await Promise.resolve(); // handle stream end
      });

      expect(mockDispatch).toHaveBeenCalledWith(setConnectionState("disconnected"));
      expect(mockClient.listSessions).toHaveBeenCalled();
      expect(onReconnect).toHaveBeenCalled();

      // Wait for backoff (1000ms)
      await act(async () => {
        jest.advanceTimersByTime(1001);
      });

      expect(streamCount).toBe(2);
    });
  });

  // ── Staleness detector ───────────────────────────────────────────────────

  describe("staleness detector", () => {
    it("marks connection as stale after 15 seconds of inactivity", () => {
      renderHook(() => useSessionService({ autoWatch: true }));

      // Simulate stream started
      async function* mockStream() {
        await new Promise(() => {});
      }
      mockClient.watchSessions.mockReturnValue(mockStream());

      act(() => {
        // Staleness check runs every 5s. Needs > 15s.
        jest.advanceTimersByTime(16000);
      });

      expect(mockDispatch).toHaveBeenCalledWith(setConnectionState("stale"));
    });
  });

  describe("enabled option", () => {
    it("does not list sessions if disabled", () => {
      renderHook(() => useSessionService({ enabled: false }));
      expect(mockClient.listSessions).not.toHaveBeenCalled();
    });

    it("lists sessions when enabled becomes true", () => {
      const { rerender } = renderHook(({ enabled }) => useSessionService({ enabled }), {
        initialProps: { enabled: false },
      });

      expect(mockClient.listSessions).not.toHaveBeenCalled();

      rerender({ enabled: true });

      expect(mockClient.listSessions).toHaveBeenCalled();
    });
  });
});
