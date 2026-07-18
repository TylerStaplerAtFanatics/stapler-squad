/**
 * Tests for ShipStatusDisplay component.
 *
 * Covers:
 *  1. Shipped via PR renders "Shipped via PR" plus the PR link
 *  2. Shipped directly (no PR) renders "Shipped directly to main"
 *  3. Not shipped renders "Not yet on main"
 *  4. Branch still existing shows ahead/behind counts
 *  5. Branch deleted shows "(deleted — already merged)" instead of counts
 *  6. Error field short-circuits to just the error message
 *  7. Commit list renders every commit, newest first, instead of the single last-commit row
 *  8. Falls back to the single last-commit row when no commit list is available
 *  9. View Diff button only renders when onViewDiff is passed, and calls it on click
 */

import React from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import { ShipStatusDisplay } from "./ShipStatusDisplay";
import type { BacklogItemShipStatus, ShippedCommit } from "@/gen/session/v1/backlog_pb";

function makeCommit(overrides: Partial<ShippedCommit> = {}): ShippedCommit {
  return {
    sha: "abc1234567",
    summary: "a commit",
    authorName: "Test Author",
    ...overrides,
  } as ShippedCommit;
}

function makeStatus(overrides: Partial<BacklogItemShipStatus> = {}): BacklogItemShipStatus {
  return {
    shipped: false,
    shippedVia: "",
    prUrl: "",
    branchName: "",
    branchExists: false,
    aheadOfMain: 0,
    behindMain: 0,
    lastCommitSha: "",
    lastCommitMessage: "",
    error: "",
    commits: [],
    ...overrides,
  } as BacklogItemShipStatus;
}

describe("ShipStatusDisplay", () => {
  it("renders Shipped via PR plus the PR link", () => {
    render(
      <ShipStatusDisplay
        status={makeStatus({ shipped: true, shippedVia: "pr", prUrl: "https://github.com/example/repo/pull/42" })}
      />
    );
    expect(screen.getByText(/Shipped via PR/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "https://github.com/example/repo/pull/42" })).toHaveAttribute(
      "href",
      "https://github.com/example/repo/pull/42"
    );
  });

  it("renders Shipped directly to main when there is no PR", () => {
    render(<ShipStatusDisplay status={makeStatus({ shipped: true, shippedVia: "direct" })} />);
    expect(screen.getByText(/Shipped directly to main/)).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("renders Not yet on main when not shipped", () => {
    render(<ShipStatusDisplay status={makeStatus({ shipped: false })} />);
    expect(screen.getByText(/Not yet on main/)).toBeInTheDocument();
  });

  it("shows ahead/behind counts when the branch still exists", () => {
    render(
      <ShipStatusDisplay
        status={makeStatus({ branchName: "feature", branchExists: true, aheadOfMain: 2, behindMain: 1 })}
      />
    );
    expect(screen.getByText(/⎇ feature/)).toBeInTheDocument();
    expect(screen.getByText(/↑2 ahead/)).toBeInTheDocument();
    expect(screen.getByText(/↓1 behind/)).toBeInTheDocument();
  });

  it("shows a deleted-branch note instead of counts once the branch is gone", () => {
    render(<ShipStatusDisplay status={makeStatus({ branchName: "feature", branchExists: false })} />);
    expect(screen.getByText(/deleted — already merged/)).toBeInTheDocument();
  });

  it("short-circuits to the error message when error is set", () => {
    render(<ShipStatusDisplay status={makeStatus({ error: "no work session ever committed code" })} />);
    expect(screen.getByText("no work session ever committed code")).toBeInTheDocument();
    expect(screen.queryByText(/Shipped|Not yet on main/)).not.toBeInTheDocument();
  });

  it("renders every shipped commit, newest first, instead of the single last-commit row", () => {
    render(
      <ShipStatusDisplay
        status={makeStatus({
          lastCommitSha: "newest111",
          lastCommitMessage: "should not render — commits list takes over",
          commits: [
            makeCommit({ sha: "newest111", summary: "newest commit" }),
            makeCommit({ sha: "oldest222", summary: "oldest commit" }),
          ],
        })}
      />
    );
    expect(screen.getByText("Commits (2):")).toBeInTheDocument();
    expect(screen.getByText("newest commit")).toBeInTheDocument();
    expect(screen.getByText("oldest commit")).toBeInTheDocument();
    expect(screen.queryByText("should not render — commits list takes over")).not.toBeInTheDocument();
  });

  it("falls back to the single last-commit row when no commit list is available", () => {
    render(
      <ShipStatusDisplay status={makeStatus({ lastCommitSha: "abc1234567", lastCommitMessage: "the only commit" })} />
    );
    expect(screen.getByText("the only commit")).toBeInTheDocument();
    expect(screen.queryByText(/Commits \(/)).not.toBeInTheDocument();
  });

  it("only renders the View Diff button when onViewDiff is passed, and calls it on click", () => {
    const onViewDiff = jest.fn();
    const { rerender } = render(<ShipStatusDisplay status={makeStatus()} />);
    expect(screen.queryByTestId("ship-status-view-diff")).not.toBeInTheDocument();

    rerender(<ShipStatusDisplay status={makeStatus()} onViewDiff={onViewDiff} />);
    fireEvent.click(screen.getByTestId("ship-status-view-diff"));
    expect(onViewDiff).toHaveBeenCalledTimes(1);
  });
});
