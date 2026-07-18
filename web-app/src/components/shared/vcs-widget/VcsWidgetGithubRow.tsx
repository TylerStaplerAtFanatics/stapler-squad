"use client";

import { Check, GitPullRequest, GitPullRequestDraft, X } from "lucide-react";
import type { VcsWidgetData } from "@/lib/vcs/types";
import * as styles from "./VcsWidgetGithubRow.css";

interface VcsWidgetGithubRowProps {
  data: VcsWidgetData;
}

function ciClassName(conclusion: string): string {
  switch (conclusion) {
    case "success":
      return styles.ciSuccess;
    case "failure":
      return styles.ciFailure;
    default:
      return styles.ciPending;
  }
}

export function VcsWidgetGithubRow({ data }: VcsWidgetGithubRowProps) {
  const captureFailed = data.kind === "historical" && data.snapshotCaptureFailed === true;

  if (!data.github && !captureFailed) return null;

  if (!data.github) {
    // Minimal placeholder — Story 4.2.1 owns the full "couldn't capture PR
    // status" failure copy for this branch. This story only guarantees the
    // component doesn't collapse to null here, so that later story can
    // extend the render without a prop-shape change.
    return (
      <div className={styles.container}>
        <span className={styles.captureFailed}>GitHub status unavailable</span>
      </div>
    );
  }

  const github = data.github;
  const PrIcon = github.isDraft ? GitPullRequestDraft : GitPullRequest;

  return (
    <div className={styles.container}>
      <a href={github.prUrl} target="_blank" rel="noopener noreferrer" className={styles.prLink}>
        <PrIcon aria-hidden="true" size={14} />
        PR #{github.prNumber}
      </a>
      {github.isDraft && <span className={styles.draftBadge}>Draft</span>}

      {(github.approvedCount > 0 || github.changesReqCount > 0) && (
        <span className={styles.reviewCounts}>
          {github.approvedCount > 0 && (
            <span className={styles.approved} aria-label={`${github.approvedCount} approved`}>
              <Check aria-hidden="true" size={14} />
              {github.approvedCount}
            </span>
          )}
          {github.changesReqCount > 0 && (
            <span
              className={styles.changesRequested}
              aria-label={`${github.changesReqCount} changes requested`}
            >
              <X aria-hidden="true" size={14} />
              {github.changesReqCount}
            </span>
          )}
        </span>
      )}

      {github.checkConclusion && (
        <span className={ciClassName(github.checkConclusion)}>CI: {github.checkConclusion}</span>
      )}
    </div>
  );
}
