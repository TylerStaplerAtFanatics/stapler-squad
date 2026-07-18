"use client";

import type { BacklogItemShipStatus, ShippedCommit } from "@/gen/session/v1/backlog_pb";
import * as styles from "./ShipStatusDisplay.css";

interface ShipStatusDisplayProps {
  status: BacklogItemShipStatus;
  onViewDiff?: () => void;
}

function formatTimestamp(ts?: { seconds: bigint }): string {
  if (!ts) return "";
  return new Date(Number(ts.seconds) * 1000).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/**
 * Historical "did this actually ship" display for a backlog item, backed by
 * GetBacklogItemShipStatus. Fallback for the Version Control section once the
 * live VCSStatus widget has nothing to show (the normal state once a done
 * item's work-session worktree has been cleaned up).
 */
export function ShipStatusDisplay({ status, onViewDiff }: ShipStatusDisplayProps) {
  if (status.error) {
    return (
      <div className={styles.container}>
        <span className={styles.detail}>{status.error}</span>
      </div>
    );
  }

  return (
    <div className={styles.container}>
      <div className={styles.row}>
        <span className={styles.label}>Shipped:</span>
        {status.shipped ? (
          <span className={styles.shipped}>
            ✓ Shipped {status.shippedVia === "pr" ? "via PR" : "directly to main"}
          </span>
        ) : (
          <span className={styles.notShipped}>✦ Not yet on main</span>
        )}
        {onViewDiff && (
          <button className={styles.viewDiffButton} onClick={onViewDiff} data-testid="ship-status-view-diff">
            View Diff ↗
          </button>
        )}
      </div>

      {status.prUrl && (
        <div className={styles.row}>
          <span className={styles.label}>PR:</span>
          <a href={status.prUrl} target="_blank" rel="noopener noreferrer" className={styles.prLink}>
            {status.prUrl}
          </a>
        </div>
      )}

      {status.branchName && (
        <div className={styles.row}>
          <span className={styles.label}>Branch:</span>
          <span className={styles.branch}>⎇ {status.branchName}</span>
          {status.branchExists ? (
            <span className={styles.detail}>
              {status.aheadOfMain > 0 && `↑${status.aheadOfMain} ahead`}
              {status.aheadOfMain > 0 && status.behindMain > 0 && " · "}
              {status.behindMain > 0 && `↓${status.behindMain} behind`}
              {status.aheadOfMain === 0 && status.behindMain === 0 && "up to date with main"}
            </span>
          ) : (
            <span className={styles.detail}>(deleted — already merged)</span>
          )}
        </div>
      )}

      {status.commits.length > 0 ? (
        <div className={styles.commitList}>
          <span className={styles.label}>
            Commits ({status.commits.length}):
          </span>
          <ul className={styles.commitListItems}>
            {status.commits.map((c: ShippedCommit) => (
              <li key={c.sha} className={styles.commitListItem}>
                <span className={styles.commitSha}>{c.sha.slice(0, 7)}</span>
                <span className={styles.detail}>{c.summary}</span>
                {c.authoredAt && <span className={styles.commitDate}>{formatTimestamp(c.authoredAt)}</span>}
              </li>
            ))}
          </ul>
        </div>
      ) : (
        status.lastCommitSha && (
          <div className={styles.row}>
            <span className={styles.label}>Commit:</span>
            <span className={styles.commitSha}>{status.lastCommitSha.slice(0, 7)}</span>
            <span className={styles.detail}>{status.lastCommitMessage}</span>
          </div>
        )
      )}
    </div>
  );
}
