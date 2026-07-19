/**
 * Tests for the "Expose ID functionality in Backlog" feature
 * (item_id=693c2700-d6b8-4d98-aaa4-c0e5eb2d42c5), AC4:
 *
 *  Visiting the board view with `?item=<uuid>` must restore the detail pane
 *  on load — previously the board only wrote the `item` query param on
 *  selection but never read it back on mount, so a deep link to
 *  `/backlog/board?item=<uuid>` silently dropped the detail pane.
 */

import React from "react";
import { render, screen, act } from "@testing-library/react";
import BacklogBoardPage from "./page";

const mockPush = jest.fn();
let searchParamsValue: string | null = null;

jest.mock("next/navigation", () => ({
  useSearchParams: () => ({ get: (key: string) => (key === "item" ? searchParamsValue : null) }),
  useRouter: () => ({ push: mockPush }),
}));

jest.mock("@/components/backlog/BacklogBoard", () => ({
  BacklogBoard: () => <div data-testid="backlog-board-stub" />,
}));

jest.mock("@/components/backlog/BacklogItemDetail", () => ({
  BacklogItemDetail: ({ itemId }: { itemId: string }) => (
    <div data-testid="backlog-item-detail-stub">{itemId}</div>
  ),
}));

const listBacklogItems = jest.fn().mockResolvedValue([]);

jest.mock("@/lib/hooks/useBacklogService", () => ({
  useBacklogService: () => ({
    listBacklogItems,
    transitionStatus: jest.fn(),
    triggerTriage: jest.fn(),
    spawnSessionFromItem: jest.fn(),
    cancelTriage: jest.fn(),
  }),
}));

jest.mock("@/lib/contexts/NotificationContext", () => ({
  useNotifications: () => ({ showActionToast: jest.fn() }),
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

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("BacklogBoardPage — deep-link restore on mount", () => {
  beforeEach(() => {
    mockPush.mockClear();
    listBacklogItems.mockClear();
    searchParamsValue = null;
  });

  it("AC4 — does not render the detail pane when no ?item= param is present", async () => {
    render(<BacklogBoardPage />);
    await flush();

    expect(screen.queryByTestId("backlog-item-detail-stub")).not.toBeInTheDocument();
  });

  it("AC4 — restores the detail pane on initial mount when ?item=<uuid> is present", async () => {
    searchParamsValue = ITEM_ID;
    render(<BacklogBoardPage />);
    await flush();

    const detail = screen.getByTestId("backlog-item-detail-stub");
    expect(detail).toBeVisible();
    expect(detail).toHaveTextContent(ITEM_ID);
  });
});
