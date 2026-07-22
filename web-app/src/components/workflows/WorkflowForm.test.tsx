import React from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import { WorkflowForm } from "./WorkflowForm";

jest.mock("@/lib/contexts/AnalyticsContext", () => ({
  useAnalytics: () => ({ track: jest.fn() }),
}));

jest.mock("@/lib/hooks/useSlashCommands", () => ({
  useSlashCommands: () => ({ commands: [], isLoading: false }),
}));

jest.mock("@/components/ui/RepoPathInput", () => ({
  RepoPathInput: ({ id, value, onChange }: { id?: string; value: string; onChange: (v: string) => void }) => (
    <input id={id} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

function fillRequiredFields() {
  fireEvent.change(screen.getByLabelText(/^Slug/), { target: { value: "my-workflow" } });
  fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: "My Workflow" } });
  fireEvent.change(screen.getByLabelText(/Command \/ Prompt/), { target: { value: "echo hi" } });
  fireEvent.change(screen.getByLabelText(/Target Directory/), { target: { value: "/tmp/x" } });
}

describe("WorkflowForm", () => {
  it("blocks submit and shows an inline error when cron is enabled with an invalid expression, without calling onSubmit", async () => {
    const onSubmit = jest.fn();
    render(<WorkflowForm onSubmit={onSubmit} onCancel={jest.fn()} />);
    fillRequiredFields();

    fireEvent.click(screen.getByTestId("cron-mode-advanced"));
    fireEvent.change(screen.getByTestId("cron-advanced-input"), { target: { value: "0 9 L * *" } });
    fireEvent.click(screen.getByLabelText("Enable scheduled runs"));

    fireEvent.click(screen.getByRole("button", { name: "Create Workflow" }));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(await screen.findByText(/Invalid cron expression/)).toBeInTheDocument();
  });

  it("submits when cron is enabled with a valid expression", async () => {
    const onSubmit = jest.fn().mockResolvedValue(undefined);
    render(<WorkflowForm onSubmit={onSubmit} onCancel={jest.fn()} />);
    fillRequiredFields();

    fireEvent.click(screen.getByTestId("cron-mode-advanced"));
    fireEvent.change(screen.getByTestId("cron-advanced-input"), { target: { value: "0 9 * * 1-5" } });
    fireEvent.click(screen.getByLabelText("Enable scheduled runs"));

    fireEvent.click(screen.getByRole("button", { name: "Create Workflow" }));

    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("does not block submit when cron is disabled, even with an invalid expression left in the field", async () => {
    const onSubmit = jest.fn().mockResolvedValue(undefined);
    render(<WorkflowForm onSubmit={onSubmit} onCancel={jest.fn()} />);
    fillRequiredFields();

    fireEvent.click(screen.getByTestId("cron-mode-advanced"));
    fireEvent.change(screen.getByTestId("cron-advanced-input"), { target: { value: "0 9 L * *" } });
    // cronEnabled checkbox left unchecked (default false).

    fireEvent.click(screen.getByRole("button", { name: "Create Workflow" }));

    expect(onSubmit).toHaveBeenCalledTimes(1);
  });
});
