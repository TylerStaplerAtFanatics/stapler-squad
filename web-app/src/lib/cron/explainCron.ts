// Client-side cron grammar pinned to the backend's parser: robfig/cron/v3 with
// `cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow` (server/workflows/scheduler.go).
// That parser bitmask omits `cron.Descriptor`, so `@daily`/`@every` etc. are rejected by the
// server despite being valid robfig syntax in general — this validator rejects them too.
// Quartz-only tokens (`?`, `L`, `W`, `#`) and a seconds field are also unsupported here.

const MONTH_NAMES: Record<string, number> = {
  jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6,
  jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12,
};
const DOW_NAMES: Record<string, number> = {
  sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6,
};
const MONTH_NAMES_REV = ["", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const DOW_NAMES_REV = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export interface CronValidationResult {
  valid: boolean;
  error?: string;
}

function isExact(token: string): boolean {
  return /^\d+$/.test(token);
}

function validateSingle(token: string, min: number, max: number, names?: Record<string, number>): boolean {
  const lower = token.toLowerCase();
  if (names && lower in names) return true;
  if (!isExact(token)) return false;
  const n = Number(token);
  return n >= min && n <= max;
}

function validateRangePart(part: string, min: number, max: number, names?: Record<string, number>): boolean {
  const [base, step, extra] = part.split("/");
  if (extra !== undefined) return false; // more than one "/"
  if (step !== undefined && (!/^\d+$/.test(step) || Number(step) <= 0)) return false;
  if (base === "*") return true;
  const bounds = base.split("-");
  if (bounds.length === 1) return validateSingle(bounds[0], min, max, names);
  if (bounds.length === 2) return validateSingle(bounds[0], min, max, names) && validateSingle(bounds[1], min, max, names);
  return false;
}

function validateField(field: string, min: number, max: number, names?: Record<string, number>): boolean {
  if (!field) return false;
  return field.split(",").every((part) => validateRangePart(part, min, max, names));
}

export function validateCron(expr: string): CronValidationResult {
  const trimmed = expr.trim();
  if (!trimmed) return { valid: false, error: "Cron expression is required" };
  if (trimmed.startsWith("@")) {
    return { valid: false, error: "Macros like @daily are not supported — use 5-field syntax (minute hour day month weekday)" };
  }
  const fields = trimmed.split(/\s+/);
  if (fields.length !== 5) {
    return { valid: false, error: `Expected 5 fields (minute hour day-of-month month day-of-week), got ${fields.length}` };
  }
  const [minute, hour, dom, month, dow] = fields;
  if (!validateField(minute, 0, 59)) return { valid: false, error: "Invalid minute field (0-59)" };
  if (!validateField(hour, 0, 23)) return { valid: false, error: "Invalid hour field (0-23)" };
  if (!validateField(dom, 1, 31)) return { valid: false, error: "Invalid day-of-month field (1-31)" };
  if (!validateField(month, 1, 12, MONTH_NAMES)) return { valid: false, error: "Invalid month field (1-12 or JAN-DEC)" };
  if (!validateField(dow, 0, 6, DOW_NAMES)) return { valid: false, error: "Invalid day-of-week field (0-6 or SUN-SAT)" };
  return { valid: true };
}

function formatClock(hour: number, minute: number): string {
  const h12 = hour % 12 === 0 ? 12 : hour % 12;
  const ampm = hour < 12 ? "AM" : "PM";
  return `${h12}:${String(minute).padStart(2, "0")} ${ampm}`;
}

function describeGeneric(field: string, namesRev?: string[]): string {
  const describeToken = (token: string): string => {
    const [base, step] = token.split("/");
    const bounds = base.split("-");
    const name = (n: string) => (namesRev && isExact(n) ? namesRev[Number(n)] ?? n : n);
    let text: string;
    if (base === "*") text = "every";
    else if (bounds.length === 2) text = `${name(bounds[0])} to ${name(bounds[1])}`;
    else text = name(bounds[0]);
    if (step !== undefined) text += ` every ${step}`;
    return text;
  };
  return field.split(",").map(describeToken).join(", ");
}

function describeMinuteHour(minute: string, hour: string): string {
  if (isExact(minute) && isExact(hour)) {
    return `at ${formatClock(Number(hour), Number(minute))}`;
  }
  return `at minute ${describeGeneric(minute)}, hour ${describeGeneric(hour)}`;
}

function describeSchedule(minute: string, hour: string, dom: string, month: string, dow: string): string {
  const time = describeMinuteHour(minute, hour);
  const domAny = dom === "*";
  const dowAny = dow === "*";
  const monthAny = month === "*";

  let schedulePart: string;
  if (domAny && dowAny) {
    schedulePart = "Every day";
  } else if (!domAny && dowAny) {
    schedulePart = `On day ${describeGeneric(dom)} of the month`;
  } else if (domAny && !dowAny) {
    schedulePart = `Every ${describeGeneric(dow, DOW_NAMES_REV)}`;
  } else {
    // Both day-of-month and day-of-week restricted: robfig/cron (like standard cron) treats
    // this as OR, not AND — "day 15 of the month" is NOT the same as "the 15th, if it's a Monday".
    schedulePart = `On day ${describeGeneric(dom)} of the month, OR every ${describeGeneric(dow, DOW_NAMES_REV)}`;
  }
  if (!monthAny) {
    schedulePart += `, in ${describeGeneric(month, MONTH_NAMES_REV)}`;
  }
  return `${schedulePart}, ${time}`;
}

export type CronExplanation =
  | { status: "empty" }
  | { status: "incomplete" }
  | { status: "error"; error: string }
  | { status: "ok"; text: string };

export function explainCron(expr: string): CronExplanation {
  const trimmed = expr.trim();
  if (!trimmed) return { status: "empty" };
  if (trimmed.split(/\s+/).length < 5 && !trimmed.startsWith("@")) return { status: "incomplete" };
  const result = validateCron(trimmed);
  if (!result.valid) return { status: "error", error: result.error ?? "Invalid cron expression" };
  const [minute, hour, dom, month, dow] = trimmed.split(/\s+/);
  return { status: "ok", text: describeSchedule(minute, hour, dom, month, dow) };
}

// --- Simple builder: only covers schedules exactly representable without steps/ranges/lists ---

export type SimpleFrequency = "daily" | "weekdays" | "weekly" | "monthly";

export interface SimpleSchedule {
  frequency: SimpleFrequency;
  hour: number;
  minute: number;
  dayOfWeek: number; // 0-6, used when frequency === "weekly"
  dayOfMonth: number; // 1-31, used when frequency === "monthly"
}

export function buildCronFromSimple(s: SimpleSchedule): string {
  const m = s.minute;
  const h = s.hour;
  switch (s.frequency) {
    case "daily": return `${m} ${h} * * *`;
    case "weekdays": return `${m} ${h} * * 1-5`;
    case "weekly": return `${m} ${h} * * ${s.dayOfWeek}`;
    case "monthly": return `${m} ${h} ${s.dayOfMonth} * *`;
  }
}

// Returns null when the expression uses syntax (steps, ranges other than 1-5, lists, month
// restrictions) the Simple builder can't represent — callers must fall back to raw editing
// rather than guessing, so an Advanced→Simple switch never silently mangles the expression.
export function parseCronToSimple(expr: string): SimpleSchedule | null {
  if (!validateCron(expr).valid) return null;
  const [minute, hour, dom, month, dow] = expr.trim().split(/\s+/);
  if (!isExact(minute) || !isExact(hour) || month !== "*") return null;
  const m = Number(minute);
  const h = Number(hour);
  if (dom === "*" && dow === "*") {
    return { frequency: "daily", hour: h, minute: m, dayOfWeek: 1, dayOfMonth: 1 };
  }
  if (dom === "*" && dow === "1-5") {
    return { frequency: "weekdays", hour: h, minute: m, dayOfWeek: 1, dayOfMonth: 1 };
  }
  if (dom === "*" && isExact(dow)) {
    return { frequency: "weekly", hour: h, minute: m, dayOfWeek: Number(dow), dayOfMonth: 1 };
  }
  if (dow === "*" && isExact(dom)) {
    return { frequency: "monthly", hour: h, minute: m, dayOfWeek: 1, dayOfMonth: Number(dom) };
  }
  return null;
}
