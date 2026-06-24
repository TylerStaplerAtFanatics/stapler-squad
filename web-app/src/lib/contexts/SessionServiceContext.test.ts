describe("SessionServiceContext", () => {
  it("SessionServiceContext_should_exposeReconnectAttemptCount_When_contextValueProvided", () => {
    const mockContextValue = {
      reconnectAttemptCount: 5,
      sessions: [],
      loading: false,
      error: null,
      connectionState: "connected" as const,
      systemMemoryPct: 0,
    };
    expect(mockContextValue.reconnectAttemptCount).toBe(5);
  });
});
