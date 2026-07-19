/**
 * Tests for the "Expose ID functionality in Backlog" feature
 * (item_id=693c2700-d6b8-4d98-aaa4-c0e5eb2d42c5):
 *
 *  AC0. The item's ID is rendered as visible, selectable text.
 *  AC1. Clicking "Copy ID" copies the raw UUID to the clipboard and shows a
 *       visible copy-confirmation state that reverts after a short delay.
 *  AC2. Clicking "Copy link" copies a shareable `/backlog?item=<uuid>` deep
 *       link to the clipboard and shows the same confirmation state.
 */

import React from "react";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { BacklogItemDetail } from "./BacklogItemDetail";
import type { BacklogItem } from "@/lib/hooks/useBacklogService";

// Heavy children pull their own hooks/timers; stub them out so this test is
// focused on BacklogItemDetail's own render behavior. Mirrors the mocking
// pattern in BacklogItemDetail.test.tsx / BacklogItemDetail.regression.test.tsx.
jest.mock("./SessionMonitor", () => ({ SessionMonitor: () => null }));
jest.mock("./GateVerdictBox", () => ({ GateVerdictBox: () => null }));
jest.mock("./TriageReviewPanel", () => ({ TriageReviewPanel: () => null }));
jest.mock("./TriageLoadingIndicator", () => ({ TriageLoadingIndicator: () => null }));

jest.mock("@/lib/hooks/useSessionRepoPaths", () => ({
  useSessionRepoPaths: () => [],
}));
jest.mock("@/lib/hooks/usePathCompletions", () => ({
  usePathCompletions: () => ({ entries: [], isLoading: false }),
}));

jest.mock("@/lib/hooks/useSessionService", () => ({
  useSessionService: () => ({ deleteSession: jest.fn() }),
}));

jest.mock("@/lib/analytics", () => ({
  useAnalytics: () => ({ track: jest.fn() }),
}));

const getBacklogItem = jest.fn();
const listPipelineModes = jest.fn().mockResolvedValue([]);

jest.mock("@/lib/hooks/useBacklogService", () => ({
  useBacklogService: () => ({
    getBacklogItem,
    transitionStatus: jest.fn().mockResolvedValue(true),
    triggerTriage: jest.fn(),
    cancelTriage: jest.fn(),
    spawnSessionFromItem: jest.fn(),
    approvePlan: jest.fn(),
    overrideVerdict: jest.fn(),
    triggerReReview: jest.fn(),
    submitManualReview: jest.fn(),
    archiveBacklogItem: jest.fn(),
    deleteBacklogItem: jest.fn(),
    updateBacklogItem: jest.fn().mockResolvedValue(null),
    listPipelineModes,
    lastError: null,
  }),
}));

// The jest styleMock for `.css.ts` files wraps every export in a callable
// proxy function, which triggers a benign "Invalid value for prop className"
// React warning. Pre-existing jest/vanilla-extract mock limitation — see
// BacklogItemDetail.test.tsx for precedent.
beforeAll(() => {
  jest.spyOn(console, "error").mockImplementation(() => {});
});

afterAll(() => {
  jest.restoreAllMocks();
});

const ITEM_ID = "693c2700-d6b8-4d98-aaa4-c0e5eb2d42c5";

const baseItem: BacklogItem = {
  id: ITEM_ID,
  title: "Expose ID functionality in Backlog",
  description: "desc",
  status: "review",
  priority: 3,
  repoPath: "/tmp/repo",
  skipPlanning: false,
  skipReviewGate: false,
  autoSpawnSession: false,
  autoCreatePR: false,
  planApproved: false,
  acCriteria: [],
  linkedSessions: [],
  notes: "",
  createdAt: "2026-07-12T14:02:00Z",
  updatedAt: "2026-07-12T14:02:00Z",
  statusEvents: [],
  totalEstimatedCostUsd: 0,
};

async function renderDetail() {
  getBacklogItem.mockReset().mockResolvedValue(baseItem);
  listPipelineModes.mockReset().mockResolvedValue([]);

  render(<BacklogItemDetail itemId={ITEM_ID} />);

  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("BacklogItemDetail — item ID & deep-link copy affordances", () => {
  const writeText = jest.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    writeText.mockClear();
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
      writable: true,
    });
    // jsdom defaults window.location to http://localhost/ — no need to mock it.
  });

  it("AC0 — renders the item's ID as visible, selectable text", async () => {
    await renderDetail();

    const idText = screen.getByTestId("backlog-item-id");
    expect(idText).toBeVisible();
    expect(idText).toHaveTextContent(ITEM_ID);
  });

  it("AC1 — clicking Copy ID copies the raw UUID and shows a confirmation that reverts", async () => {
    jest.useFakeTimers();
    await renderDetail();

    const copyIdButton = screen.getByTestId("copy-backlog-id");
    fireEvent.click(copyIdButton);

    expect(writeText).toHaveBeenCalledWith(ITEM_ID);

    await waitFor(() => expect(copyIdButton).toHaveTextContent("Copied"));

    await act(async () => {
      jest.advanceTimersByTime(2000);
    });
    expect(copyIdButton).not.toHaveTextContent("Copied");

    jest.useRealTimers();
  });

  it("AC2 — clicking Copy link copies a /backlog?item=<uuid> deep link and shows a confirmation", async () => {
    await renderDetail();

    const copyLinkButton = screen.getByTestId("copy-backlog-link");
    fireEvent.click(copyLinkButton);

    expect(writeText).toHaveBeenCalledWith(
      expect.stringMatching(new RegExp(`/backlog\\?item=${ITEM_ID}$`))
    );

    await waitFor(() => expect(copyLinkButton).toHaveTextContent("Copied"));
  });
});
