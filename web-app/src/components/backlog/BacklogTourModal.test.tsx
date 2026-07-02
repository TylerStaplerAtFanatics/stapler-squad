/**
 * Tests for BacklogTourModal.
 *
 * Covers:
 *  1. Does not render step content when isOpen is false
 *  2. Renders the first step (lifecycle) when open
 *  3. Next/Back navigate between all 4 steps
 *  4. Step 2 explicitly explains the Repository Path gotcha
 *  5. Skip calls onClose and marks the tour complete in localStorage
 *  6. "Got it" on the last step calls onClose
 */

import React from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import { BacklogTourModal } from "./BacklogTourModal";
import { BACKLOG_ONBOARDED_KEY } from "./useBacklogTour";

describe("BacklogTourModal — visibility", () => {
  it("renders nothing when isOpen is false", () => {
    render(<BacklogTourModal isOpen={false} onClose={jest.fn()} />);
    expect(screen.queryByTestId("backlog-tour-modal")).not.toBeInTheDocument();
  });

  it("renders the first step when isOpen is true", () => {
    render(<BacklogTourModal isOpen onClose={jest.fn()} />);
    expect(screen.getByText("How backlog items work")).toBeInTheDocument();
  });
});

describe("BacklogTourModal — navigation", () => {
  it("Next advances through all steps, Back returns to the previous one", () => {
    render(<BacklogTourModal isOpen onClose={jest.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Filling out the form")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("What happens after you hit Create")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Skip planning / Skip review gate")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("What happens after you hit Create")).toBeInTheDocument();
  });

  it("step 2 calls out the Repository Path gotcha explicitly", () => {
    render(<BacklogTourModal isOpen onClose={jest.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Next" }));

    expect(screen.getByTestId("backlog-tour-repo-path-callout")).toHaveTextContent(
      "we'll clone it for you automatically"
    );
  });
});

describe("BacklogTourModal — dismissal", () => {
  beforeEach(() => localStorage.clear());

  it("Skip calls onClose and marks the tour complete", () => {
    const onClose = jest.fn();
    render(<BacklogTourModal isOpen onClose={onClose} />);

    fireEvent.click(screen.getByRole("button", { name: "Skip tour" }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem(BACKLOG_ONBOARDED_KEY)).toBe("true");
  });

  it("'Got it' on the last step calls onClose", () => {
    const onClose = jest.fn();
    render(<BacklogTourModal isOpen onClose={onClose} />);

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.click(screen.getByRole("button", { name: "Got it" }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
