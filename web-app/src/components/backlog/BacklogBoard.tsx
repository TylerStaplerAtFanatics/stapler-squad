"use client";
// +feature: backlog:board

import type { BacklogItem, BacklogItemStatus } from "@/lib/hooks/useBacklogService";
import { useWatchBacklogItems } from "@/lib/hooks/useWatchBacklogItems";
import { BacklogItemCard } from "./BacklogItemCard";
import { ConnectionIndicator } from "./ConnectionIndicator";
import * as styles from "./BacklogBoard.css";

interface BacklogBoardProps {
  onAction: (action: string, itemId: string) => void;
  onItemClick: (itemId: string) => void;
  /** itemId -> action key currently in flight for that card. */
  pending?: Record<string, string>;
}

const COLUMNS: { status: BacklogItemStatus; label: string }[] = [
  { status: "idea", label: "Idea" },
  { status: "ready", label: "Ready" },
  { status: "in_progress", label: "In Progress" },
  { status: "review", label: "Review" },
  { status: "done", label: "Done" },
];

function SkeletonCard() {
  return (
    <div className={styles.skeletonCard} aria-hidden="true">
      <div className={styles.skeletonLine} />
      <div className={`${styles.skeletonLine} ${styles.skeletonLineShort}`} />
    </div>
  );
}

function BoardColumn({
  column,
  items,
  onAction,
  onItemClick,
  isLoading,
  pending,
}: {
  column: { status: BacklogItemStatus; label: string };
  items: BacklogItem[];
  onAction: (action: string, itemId: string) => void;
  onItemClick: (itemId: string) => void;
  isLoading: boolean;
  pending: Record<string, string>;
}) {
  return (
    <section
      className={styles.column}
      aria-label={`${column.label} column`}
      data-testid={`backlog-column-${column.status}`}
    >
      <div className={styles.columnHeader}>
        <h3 className={styles.columnTitle}>{column.label}</h3>
        <span className={styles.columnCount} aria-label={`${items.length} items`}>
          {items.length}
        </span>
      </div>

      <div className={styles.columnCards} role="list" aria-label={`${column.label} items`}>
        {isLoading ? (
          <>
            <SkeletonCard />
            <SkeletonCard />
          </>
        ) : items.length === 0 ? (
          <p className={styles.emptyColumn}>No items</p>
        ) : (
          items.map((item) => (
            <div key={item.id} role="listitem">
              <BacklogItemCard
                item={item}
                onAction={onAction}
                onClick={onItemClick}
                pendingAction={pending[item.id] ?? null}
              />
            </div>
          ))
        )}
      </div>
    </section>
  );
}

export function BacklogBoard({
  onAction,
  onItemClick,
  pending = {},
}: BacklogBoardProps) {
  // Epic 5.2 (backlog-event-driven-updates): the board subscribes to the
  // same live stream/normalized store as the list page (ux.md §2, "no
  // board-specific fetch") rather than receiving items as props — a status-
  // change event moves an item's column membership purely by this filter
  // re-evaluating on the updated item, no board-specific refetch involved.
  const { items, connectionState } = useWatchBacklogItems();
  // Only show the skeleton on a genuinely empty first paint — a disconnect/
  // reconnect must keep showing last-known state, not blank/spinner-out
  // (ux.md §1 "Error / edge cases", shared by this surface per §2).
  const isLoading = connectionState === "connecting" && items.length === 0;

  return (
    <div className={styles.boardWrapper}>
      {/* Task 6.2.1c: one ConnectionIndicator per board, not per column
          (ux.md §2 "Interaction flow" #5, UX AC #9). */}
      <div className={styles.boardToolbar}>
        <ConnectionIndicator connectionState={connectionState} />
      </div>
      <div
        className={styles.board}
        role="region"
        aria-label="Backlog board"
        data-testid="backlog-board"
      >
        {COLUMNS.map((column) => (
          <BoardColumn
            key={column.status}
            column={column}
            items={items.filter((i) => i.status === column.status)}
            onAction={onAction}
            onItemClick={onItemClick}
            isLoading={isLoading}
            pending={pending}
          />
        ))}
      </div>
    </div>
  );
}
