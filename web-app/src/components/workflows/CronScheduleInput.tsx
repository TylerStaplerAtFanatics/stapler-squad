"use client";
// +feature: workflow-cron-schedule-input

import { useDeferredValue, useMemo, useState } from "react";
import {
  explainCron,
  parseCronToSimple,
  buildCronFromSimple,
} from "@/lib/cron/explainCron";
import type { SimpleFrequency, SimpleSchedule } from "@/lib/cron/explainCron";
import { useAnalytics } from "@/lib/analytics";
import * as styles from "./CronScheduleInput.css";
import * as formStyles from "./WorkflowForm.css";

interface CronScheduleInputProps {
  value: string;
  onChange: (value: string) => void;
  id?: string;
}

const DEFAULT_SIMPLE: SimpleSchedule = { frequency: "daily", hour: 9, minute: 0, dayOfWeek: 1, dayOfMonth: 1 };

const DOW_OPTIONS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function timeToParts(time: string): { hour: number; minute: number } | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec(time);
  if (!match) return null;
  return { hour: Number(match[1]), minute: Number(match[2]) };
}

function partsToTime(hour: number, minute: number): string {
  return `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
}

export function CronScheduleInput({ value, onChange, id = "wf-cron" }: CronScheduleInputProps) {
  const [mode, setMode] = useState<"simple" | "advanced">(
    () => (value.trim() === "" || parseCronToSimple(value) !== null ? "simple" : "advanced"),
  );
  const [simple, setSimple] = useState<SimpleSchedule>(() => parseCronToSimple(value) ?? DEFAULT_SIMPLE);
  const [fallbackNotice, setFallbackNotice] = useState(false);
  const { track } = useAnalytics();

  const deferredValue = useDeferredValue(value);
  const explanation = useMemo(() => explainCron(deferredValue), [deferredValue]);

  function selectMode(target: "simple" | "advanced") {
    track({ name: "cron_schedule_mode_toggle", category: "user_action", component: "CronScheduleInput", labels: { target } });
    if (target === "advanced") {
      setMode("advanced");
      setFallbackNotice(false);
      return;
    }
    const parsed = parseCronToSimple(value);
    if (!parsed) {
      // Don't silently mangle an expression the builder can't represent (AC3) — stay on
      // Advanced and surface a notice instead of switching.
      setFallbackNotice(true);
      return;
    }
    setFallbackNotice(false);
    setSimple(parsed);
    setMode("simple");
  }

  function updateSimple(next: SimpleSchedule) {
    setSimple(next);
    onChange(buildCronFromSimple(next));
  }

  return (
    <div className={styles.wrapper}>
      <div className={styles.modeGroup} role="radiogroup" aria-label="Schedule input mode">
        <button
          type="button"
          role="radio"
          aria-checked={mode === "simple"}
          data-testid="cron-mode-simple"
          className={`${styles.modeButton} ${mode === "simple" ? styles.modeButtonActive : ""}`}
          onClick={() => selectMode("simple")}
        >
          Simple
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={mode === "advanced"}
          data-testid="cron-mode-advanced"
          className={`${styles.modeButton} ${mode === "advanced" ? styles.modeButtonActive : ""}`}
          onClick={() => selectMode("advanced")}
        >
          Advanced
        </button>
      </div>

      {fallbackNotice && (
        <p className={styles.notice} role="status" data-testid="cron-fallback-notice">
          This expression uses syntax (steps, ranges, lists) the Simple builder can&apos;t represent — edit it directly below.
        </p>
      )}

      {mode === "simple" ? (
        <div className={styles.simpleRow} data-testid="cron-simple-builder">
          <label>
            <span className={styles.srOnly}>Frequency</span>
            <select
              className={styles.select}
              data-testid="cron-simple-frequency"
              value={simple.frequency}
              onChange={(e) => updateSimple({ ...simple, frequency: e.target.value as SimpleFrequency })}
            >
              <option value="daily">Daily</option>
              <option value="weekdays">Weekdays</option>
              <option value="weekly">Weekly</option>
              <option value="monthly">Monthly</option>
            </select>
          </label>

          {simple.frequency === "weekly" && (
            <label>
              <span className={styles.srOnly}>Day of week</span>
              <select
                className={styles.select}
                data-testid="cron-simple-dow"
                value={simple.dayOfWeek}
                onChange={(e) => updateSimple({ ...simple, dayOfWeek: Number(e.target.value) })}
              >
                {DOW_OPTIONS.map((name, idx) => (
                  <option key={name} value={idx}>{name}</option>
                ))}
              </select>
            </label>
          )}

          {simple.frequency === "monthly" && (
            <label>
              <span className={styles.srOnly}>Day of month</span>
              <input
                type="number"
                min={1}
                max={31}
                className={styles.select}
                data-testid="cron-simple-dom"
                value={simple.dayOfMonth}
                onChange={(e) => updateSimple({ ...simple, dayOfMonth: Number(e.target.value) || 1 })}
              />
            </label>
          )}

          <label>
            <span className={styles.srOnly}>Time</span>
            <input
              type="time"
              className={styles.select}
              data-testid="cron-simple-time"
              value={partsToTime(simple.hour, simple.minute)}
              onChange={(e) => {
                const parts = timeToParts(e.target.value);
                if (parts) updateSimple({ ...simple, hour: parts.hour, minute: parts.minute });
              }}
            />
          </label>
        </div>
      ) : (
        <input
          id={id}
          className={formStyles.input}
          type="text"
          data-testid="cron-advanced-input"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="0 9 * * 1-5"
          aria-describedby="cron-explanation"
        />
      )}

      <p
        id="cron-explanation"
        aria-live="polite"
        role={explanation.status === "error" ? "alert" : undefined}
        data-testid="cron-explanation"
        className={
          explanation.status === "error"
            ? `${styles.errorText}`
            : explanation.status === "empty" || explanation.status === "incomplete"
              ? `${styles.explanation} ${styles.explanationEmpty}`
              : `${styles.explanation}`
        }
      >
        {explanation.status === "empty" && "Enter a schedule above to see what it means."}
        {explanation.status === "incomplete" && "Still typing…"}
        {explanation.status === "error" && `Invalid cron expression: ${explanation.error}`}
        {explanation.status === "ok" && explanation.text}
      </p>

      <span className={styles.timezoneLabel} data-testid="cron-timezone-label">
        Runs in the server&apos;s local timezone (not your browser&apos;s)
      </span>
    </div>
  );
}
