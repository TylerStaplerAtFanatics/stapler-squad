// @feature workflows-management
/**
 * E2E tests for the CronScheduleInput widget on the Workflow create/edit form.
 *
 * Prerequisites:
 *   STAPLER_SQUAD_USE_CONTROL_MODE=false STAPLER_SQUAD_INSTANCE=e2e-local \
 *   ./stapler-squad --tmux-keep-server &
 */

import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const BASE_URL = process.env.BASE_URL ?? "http://localhost:8544";

async function fillCommonFields(page: import("@playwright/test").Page, slug: string) {
  await page.getByLabel(/^Slug/).fill(slug);
  await page.getByLabel(/^Name/).fill(`E2E ${slug}`);
  await page.getByLabel(/Command \/ Prompt/).fill("echo hello");
  await page.locator("#wf-target-dir").fill("/tmp/e2e-workflow-cron-test");
}

test.describe("workflow-cron-schedule", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto(`${BASE_URL}/workflows`);
  });

  test("Advanced mode: raw cron entry still works unchanged for power users", async ({ page }) => {
    const slug = `e2e-cron-advanced-${Date.now()}`;
    await page.getByRole("button", { name: "+ New Workflow" }).click();
    await fillCommonFields(page, slug);

    await page.getByTestId("cron-mode-advanced").click();
    await page.getByTestId("cron-advanced-input").fill("0 9 * * 1-5");
    await page.getByLabel("Enable scheduled runs").check();
    await expect(page.getByTestId("cron-explanation")).toContainText("Monday to Friday");

    await page.getByRole("button", { name: "Create Workflow" }).click();

    const row = page.locator("tr", { hasText: `@${slug}` });
    await expect(row).toBeVisible();
    await expect(row).toContainText("0 9 * * 1-5");
  });

  test("blocks submission client-side when the Advanced cron expression is invalid", async ({ page }) => {
    const slug = `e2e-cron-invalid-${Date.now()}`;
    await page.getByRole("button", { name: "+ New Workflow" }).click();
    await fillCommonFields(page, slug);

    await page.getByTestId("cron-mode-advanced").click();
    // "L" is a Quartz-only token robfig/cron/v3 does not accept (unlike "?", which robfig treats
    // as a plain synonym for "*" and is therefore valid — see explainCron.ts's grammar comment).
    await page.getByTestId("cron-advanced-input").fill("0 9 L * *");
    await page.getByLabel("Enable scheduled runs").check();

    await page.getByRole("button", { name: "Create Workflow" }).click();

    // Form stays open with an inline error — no workflow row is created.
    await expect(page.getByRole("heading", { name: "New Workflow" })).toBeVisible();
    await expect(page.getByTestId("workflow-form-error")).toContainText(/Invalid/);
    await expect(page.locator(`tr:has-text("@${slug}")`)).toHaveCount(0);
  });

  test("Simple mode: building a weekly schedule via dropdowns creates the expected cron string", async ({ page }) => {
    const slug = `e2e-cron-simple-${Date.now()}`;
    await page.getByRole("button", { name: "+ New Workflow" }).click();
    await fillCommonFields(page, slug);

    await page.getByTestId("cron-simple-frequency").selectOption("weekly");
    await page.getByTestId("cron-simple-dow").selectOption("2"); // Tuesday
    await page.getByTestId("cron-simple-time").fill("13:45");
    await page.getByLabel("Enable scheduled runs").check();

    await page.getByRole("button", { name: "Create Workflow" }).click();

    const row = page.locator("tr", { hasText: `@${slug}` });
    await expect(row).toBeVisible();
    await expect(row).toContainText("45 13 * * 2");
  });

  test("has no critical or serious Axe violations in Simple or Advanced mode", async ({ page }) => {
    await page.getByRole("button", { name: "+ New Workflow" }).click();

    const simpleResults = await new AxeBuilder({ page }).include("[data-testid='cron-simple-builder']").analyze();
    const simpleViolations = simpleResults.violations.filter((v) => v.impact === "critical" || v.impact === "serious");
    expect(simpleViolations).toHaveLength(0);

    await page.getByTestId("cron-mode-advanced").click();
    const advancedResults = await new AxeBuilder({ page }).include("[data-testid='cron-advanced-input']").analyze();
    const advancedViolations = advancedResults.violations.filter((v) => v.impact === "critical" || v.impact === "serious");
    expect(advancedViolations).toHaveLength(0);
  });
});
