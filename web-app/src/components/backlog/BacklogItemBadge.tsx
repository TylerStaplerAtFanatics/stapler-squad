"use client";
// +feature: backlog:item-badge

import { getStatusLabel } from "@/lib/backlog/status";
import * as styles from "./BacklogItemBadge.css";

interface BacklogItemBadgeProps {
  itemTitle: string;
  status: string;
  acTotal: number;
  acDone: number;
}

const STATUS_CLASS: Record<string, string> = {
  idea: styles.statusIdea,
  refining: styles.statusRefining,
  ready: styles.statusReady,
  in_progress: styles.statusInProgress,
  review: styles.statusReview,
  done: styles.statusDone,
  archived: styles.statusArchived,
};

const getStatusClass = (s: string): string => STATUS_CLASS[s] ?? styles.statusArchived;

function truncate(s: string, max: number): string {
  return s.length > max ? s.slice(0, max - 1) + "…" : s;
}

export function BacklogItemBadge({
  itemTitle,
  status,
  acTotal,
  acDone,
}: BacklogItemBadgeProps) {
  return (
    <span className={styles.badge} data-testid="backlog-item-badge">
      <span
        className={`${styles.statusChip} ${getStatusClass(status)}`}
        aria-label={`Status: ${getStatusLabel(status)}`}
      >
        {getStatusLabel(status)}
      </span>
      {acTotal > 0 && (
        <span className={styles.acCount} aria-label={`${acDone} of ${acTotal} criteria done`}>
          {acDone}/{acTotal} ✓
        </span>
      )}
      <span className={styles.itemTitle} title={itemTitle}>
        {truncate(itemTitle, 40)}
      </span>
    </span>
  );
}
