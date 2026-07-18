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
 */

import React from "react";
import { render, screen } from "@testing-library/react";
import { ShipStatusDisplay } from "./ShipStatusDisplay";
import type { BacklogItemShipStatus } from "@/gen/session/v1/backlog_pb";

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
});
