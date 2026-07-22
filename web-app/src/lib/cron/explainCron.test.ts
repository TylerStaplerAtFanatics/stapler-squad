import { validateCron, explainCron, buildCronFromSimple, parseCronToSimple } from "./explainCron";

describe("validateCron", () => {
  it("accepts a plain daily expression", () => {
    expect(validateCron("0 9 * * *").valid).toBe(true);
  });

  it("accepts steps, ranges, and lists", () => {
    expect(validateCron("*/15 9-17 * * 1-5").valid).toBe(true);
    expect(validateCron("0 9,13,17 * * *").valid).toBe(true);
  });

  it("accepts month and day names", () => {
    expect(validateCron("0 9 1 JAN *").valid).toBe(true);
    expect(validateCron("0 9 * * MON").valid).toBe(true);
  });

  it("rejects macros/descriptors since the backend parser omits cron.Descriptor", () => {
    expect(validateCron("@daily").valid).toBe(false);
    expect(validateCron("@every 1h").valid).toBe(false);
  });

  it("rejects a 6-field (seconds) expression", () => {
    const result = validateCron("0 0 9 * * *");
    expect(result.valid).toBe(false);
    expect(result.error).toMatch(/5 fields/);
  });

  it("rejects Quartz-only tokens", () => {
    expect(validateCron("0 9 ? * *").valid).toBe(false);
    expect(validateCron("0 9 L * *").valid).toBe(false);
    expect(validateCron("0 9 * * 5W").valid).toBe(false);
    expect(validateCron("0 9 * * 5#3").valid).toBe(false);
  });

  it("rejects out-of-range values", () => {
    expect(validateCron("60 9 * * *").valid).toBe(false);
    expect(validateCron("0 24 * * *").valid).toBe(false);
    expect(validateCron("0 9 32 * *").valid).toBe(false);
    expect(validateCron("0 9 * 13 *").valid).toBe(false);
    expect(validateCron("0 9 * * 7").valid).toBe(false);
  });

  it("rejects empty input", () => {
    expect(validateCron("").valid).toBe(false);
    expect(validateCron("   ").valid).toBe(false);
  });
});

describe("explainCron", () => {
  it("returns empty status for blank input", () => {
    expect(explainCron("")).toEqual({ status: "empty" });
  });

  it("returns incomplete status while the user is still typing fields", () => {
    expect(explainCron("0 9 * *")).toEqual({ status: "incomplete" });
  });

  it("returns an error for malformed input without throwing", () => {
    const result = explainCron("0 9 ? * *");
    expect(result.status).toBe("error");
  });

  it("explains a daily schedule", () => {
    const result = explainCron("0 9 * * *");
    expect(result).toEqual({ status: "ok", text: "Every day, at 9:00 AM" });
  });

  it("explains a weekly schedule", () => {
    const result = explainCron("0 9 * * 1");
    expect(result).toEqual({ status: "ok", text: "Every Monday, at 9:00 AM" });
  });

  it("explains a monthly schedule", () => {
    const result = explainCron("0 9 1 * *");
    expect(result).toEqual({ status: "ok", text: "On day 1 of the month, at 9:00 AM" });
  });

  it("explains dom+dow both restricted using OR semantics, not AND", () => {
    const result = explainCron("0 9 15 * 1");
    expect(result).toEqual({
      status: "ok",
      text: "On day 15 of the month, OR every Monday, at 9:00 AM",
    });
    if (result.status === "ok") {
      expect(result.text).not.toMatch(/15th.*Monday|Monday.*15th/i);
    }
  });

  it("explains step values", () => {
    const result = explainCron("*/15 9-17 * * 1-5");
    expect(result.status).toBe("ok");
    if (result.status === "ok") {
      expect(result.text).toContain("every 15");
      expect(result.text).toContain("9 to 17");
      expect(result.text).toContain("Monday to Friday");
    }
  });

  it("explains list values", () => {
    const result = explainCron("0 9,13,17 * * *");
    expect(result.status).toBe("ok");
    if (result.status === "ok") {
      expect(result.text).toContain("9, 13, 17");
    }
  });
});

describe("buildCronFromSimple / parseCronToSimple round trip", () => {
  it("builds and parses daily", () => {
    const cron = buildCronFromSimple({ frequency: "daily", hour: 9, minute: 0, dayOfWeek: 1, dayOfMonth: 1 });
    expect(cron).toBe("0 9 * * *");
    expect(parseCronToSimple(cron)).toEqual({ frequency: "daily", hour: 9, minute: 0, dayOfWeek: 1, dayOfMonth: 1 });
  });

  it("builds and parses weekdays", () => {
    const cron = buildCronFromSimple({ frequency: "weekdays", hour: 8, minute: 30, dayOfWeek: 1, dayOfMonth: 1 });
    expect(cron).toBe("30 8 * * 1-5");
    expect(parseCronToSimple(cron)?.frequency).toBe("weekdays");
  });

  it("builds and parses weekly", () => {
    const cron = buildCronFromSimple({ frequency: "weekly", hour: 9, minute: 0, dayOfWeek: 3, dayOfMonth: 1 });
    expect(cron).toBe("0 9 * * 3");
    expect(parseCronToSimple(cron)).toEqual({ frequency: "weekly", hour: 9, minute: 0, dayOfWeek: 3, dayOfMonth: 1 });
  });

  it("builds and parses monthly", () => {
    const cron = buildCronFromSimple({ frequency: "monthly", hour: 22, minute: 15, dayOfWeek: 1, dayOfMonth: 28 });
    expect(cron).toBe("15 22 28 * *");
    expect(parseCronToSimple(cron)).toEqual({ frequency: "monthly", hour: 22, minute: 15, dayOfWeek: 1, dayOfMonth: 28 });
  });

  it("returns null for expressions the builder can't represent", () => {
    expect(parseCronToSimple("*/15 9-17 * * 1-5")).toBeNull();
    expect(parseCronToSimple("0 9,13,17 * * *")).toBeNull();
    expect(parseCronToSimple("0 9 * JAN *")).toBeNull();
    expect(parseCronToSimple("0 9 15 * 1")).toBeNull(); // dom+dow both restricted
  });
});
