import React from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import { NotificationToast } from "./NotificationToast";
import type { NotificationData } from "@/lib/types/notification";

const mockPush = jest.fn();
jest.mock("next/navigation", () => ({ useRouter: () => ({ push: mockPush }) }));

const mockLogNotificationSessionViewed = jest.fn();
const mockLogNotificationBacklogItemViewed = jest.fn();
const mockLogNotificationDismissed = jest.fn();
jest.mock("@/lib/hooks/useAuditLog", () => ({
  useAuditLog: () => ({
    logNotificationDismissed: mockLogNotificationDismissed,
    logNotificationSessionViewed: mockLogNotificationSessionViewed,
    logNotificationBacklogItemViewed: mockLogNotificationBacklogItemViewed,
  }),
}));

// Stub CSS module so class names are plain strings (valid React DOM props).
jest.mock("./NotificationToast.css", () => {
  return new Proxy(
    {},
    { get: (_target, prop) => String(prop) }
  );
});

function makeNotification(overrides: Partial<NotificationData> = {}): NotificationData {
  return {
    id: "n-1",
    sessionId: "s-1",
    sessionName: "Test Session",
    message: "Something happened",
    timestamp: Date.now(),
    notificationType: "info",
    priority: 2,
    ...overrides,
  };
}

beforeEach(() => {
  jest.clearAllMocks();
});

describe("NotificationToast", () => {
  describe("button rendering", () => {
    it("shows 'View Session' when metadata has no item_id", () => {
      render(<NotificationToast notification={makeNotification()} onClose={jest.fn()} />);
      expect(screen.getByText("View Session")).toBeInTheDocument();
      expect(screen.queryByText("View Backlog")).not.toBeInTheDocument();
    });

    it("shows 'View Backlog' when metadata contains item_id", () => {
      const n = makeNotification({ metadata: { item_id: "abc-123" } });
      render(<NotificationToast notification={n} onClose={jest.fn()} />);
      expect(screen.getByText("View Backlog")).toBeInTheDocument();
      expect(screen.queryByText("View Session")).not.toBeInTheDocument();
    });

    it("shows 'View Session' when metadata exists but item_id is absent", () => {
      const n = makeNotification({ metadata: { other_key: "value" } });
      render(<NotificationToast notification={n} onClose={jest.fn()} />);
      expect(screen.getByText("View Session")).toBeInTheDocument();
    });
  });

  describe("handleViewBacklog", () => {
    it("navigates to /backlog?item=<encoded-id>", () => {
      const itemId = "abc-123";
      const n = makeNotification({ metadata: { item_id: itemId } });
      render(<NotificationToast notification={n} onClose={jest.fn()} />);

      fireEvent.click(screen.getByText("View Backlog"));

      expect(mockPush).toHaveBeenCalledWith(
        `/backlog?item=${encodeURIComponent(itemId)}`
      );
    });

    it("logs backlog item viewed, not session viewed", () => {
      const itemId = "abc-123";
      const n = makeNotification({ metadata: { item_id: itemId } });
      render(<NotificationToast notification={n} onClose={jest.fn()} />);

      fireEvent.click(screen.getByText("View Backlog"));

      expect(mockLogNotificationBacklogItemViewed).toHaveBeenCalledWith("n-1", itemId);
      expect(mockLogNotificationSessionViewed).not.toHaveBeenCalled();
    });

    it("encodes special characters in item_id", () => {
      const itemId = "item with spaces & chars";
      const n = makeNotification({ metadata: { item_id: itemId } });
      render(<NotificationToast notification={n} onClose={jest.fn()} />);

      fireEvent.click(screen.getByText("View Backlog"));

      expect(mockPush).toHaveBeenCalledWith(
        `/backlog?item=${encodeURIComponent(itemId)}`
      );
    });
  });
});
