import React from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import { CronScheduleInput } from "./CronScheduleInput";

jest.mock("@/lib/contexts/AnalyticsContext", () => ({
  useAnalytics: () => ({ track: jest.fn() }),
}));

function Wrapper({ initial }: { initial: string }) {
  const [value, setValue] = React.useState(initial);
  return (
    <>
      <CronScheduleInput value={value} onChange={setValue} />
      <output data-testid="current-value">{value}</output>
    </>
  );
}

describe("CronScheduleInput", () => {
  it("starts in Simple mode for an empty value and shows the empty-state explanation", () => {
    render(<Wrapper initial="" />);
    expect(screen.getByTestId("cron-mode-simple")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("cron-explanation")).toHaveTextContent("Enter a schedule above");
  });

  it("Simple mode: daily frequency + time produces the correct cron string", () => {
    render(<Wrapper initial="" />);
    fireEvent.change(screen.getByTestId("cron-simple-time"), { target: { value: "14:05" } });
    expect(screen.getByTestId("current-value")).toHaveTextContent("5 14 * * *");
  });

  it("Simple mode: weekly frequency exposes a day-of-week select and produces the correct cron string", () => {
    render(<Wrapper initial="" />);
    fireEvent.change(screen.getByTestId("cron-simple-frequency"), { target: { value: "weekly" } });
    fireEvent.change(screen.getByTestId("cron-simple-dow"), { target: { value: "3" } });
    fireEvent.change(screen.getByTestId("cron-simple-time"), { target: { value: "11:20" } });
    expect(screen.getByTestId("current-value")).toHaveTextContent("20 11 * * 3");
  });

  it("Simple mode: weekdays frequency produces a 1-5 range", () => {
    render(<Wrapper initial="" />);
    fireEvent.change(screen.getByTestId("cron-simple-frequency"), { target: { value: "weekdays" } });
    fireEvent.change(screen.getByTestId("cron-simple-time"), { target: { value: "08:30" } });
    expect(screen.getByTestId("current-value")).toHaveTextContent("30 8 * * 1-5");
  });

  it("Simple mode: monthly frequency exposes a day-of-month input", () => {
    render(<Wrapper initial="" />);
    fireEvent.change(screen.getByTestId("cron-simple-frequency"), { target: { value: "monthly" } });
    fireEvent.change(screen.getByTestId("cron-simple-dom"), { target: { value: "15" } });
    fireEvent.change(screen.getByTestId("cron-simple-time"), { target: { value: "09:00" } });
    expect(screen.getByTestId("current-value")).toHaveTextContent("0 9 15 * *");
  });

  it("shows an inline alert for an invalid Advanced expression", () => {
    render(<Wrapper initial="0 9 * * *" />);
    fireEvent.click(screen.getByTestId("cron-mode-advanced"));
    fireEvent.change(screen.getByTestId("cron-advanced-input"), { target: { value: "0 9 ? * *" } });
    const explanation = screen.getByTestId("cron-explanation");
    expect(explanation).toHaveAttribute("role", "alert");
    expect(explanation).toHaveTextContent("Invalid cron expression");
  });

  it("starts in Advanced mode for an expression the builder can't represent, and toggling to Simple shows a fallback notice instead of switching", () => {
    render(<Wrapper initial="*/15 9-17 * * 1-5" />);
    expect(screen.getByTestId("cron-mode-advanced")).toHaveAttribute("aria-checked", "true");
    fireEvent.click(screen.getByTestId("cron-mode-simple"));
    expect(screen.getByTestId("cron-fallback-notice")).toBeInTheDocument();
    expect(screen.getByTestId("cron-mode-advanced")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("cron-advanced-input")).toHaveValue("*/15 9-17 * * 1-5");
  });

  it("Simple -> Advanced toggle preserves the exact builder-computed cron string", () => {
    render(<Wrapper initial="" />);
    fireEvent.change(screen.getByTestId("cron-simple-frequency"), { target: { value: "weekly" } });
    fireEvent.change(screen.getByTestId("cron-simple-dow"), { target: { value: "5" } });
    fireEvent.change(screen.getByTestId("cron-simple-time"), { target: { value: "17:45" } });
    expect(screen.getByTestId("current-value")).toHaveTextContent("45 17 * * 5");
    fireEvent.click(screen.getByTestId("cron-mode-advanced"));
    expect(screen.getByTestId("cron-advanced-input")).toHaveValue("45 17 * * 5");
  });

  it("shows a server-local timezone label", () => {
    render(<Wrapper initial="" />);
    expect(screen.getByTestId("cron-timezone-label")).toHaveTextContent(/server/i);
  });
});
