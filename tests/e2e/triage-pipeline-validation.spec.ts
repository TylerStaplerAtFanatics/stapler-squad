// @feature backlog:triage
// Validates the end-to-end triage pipeline:
//   1. Create item with a real repo path
//   2. Trigger triage
//   3. Confirm loading indicator appears (session started and received prompt)
//   4. Wait for triage-review-panel (session completed and submitted results)
//
// Runs against the live server. Set TRIAGE_VALIDATION=true to enable.
import { test, expect } from '@playwright/test';

const BASE_URL = process.env.TEST_SERVER_URL || 'http://localhost:8543';
const REPO_PATH = process.env.TRIAGE_REPO_PATH || '/Users/tylerstapler/IdeaProjects/stapler-squad';

test.describe('Triage pipeline validation', () => {
  test.skip(
    process.env.TRIAGE_VALIDATION !== 'true',
    'Set TRIAGE_VALIDATION=true to run this live integration test'
  );

  test.setTimeout(1_200_000); // 20 minutes — real Claude triage with parallel subagents takes 12-15 min

  test('e2e:triage-pipeline - triage starts, receives prompt, completes, shows review panel', async ({ page }) => {
    await page.goto(`${BASE_URL}/backlog`, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('[data-testid="backlog-page"]', { timeout: 15_000 });

    // Dismiss any open overlays/dialogs left from a previous session.
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    await page.waitForFunction(
      () => document.querySelectorAll('[data-state="open"][aria-hidden="true"]').length === 0,
      { timeout: 5_000 }
    ).catch(() => {});

    // --- 1. Open the new-item modal ---
    const newItemBtn = page.locator('[data-testid="backlog-new-item-button"]');
    await expect(newItemBtn).toBeVisible({ timeout: 5_000 });
    await newItemBtn.click({ force: true });
    await page.waitForSelector('[data-testid="backlog-form-modal"]', { timeout: 5_000 });

    const title = `triage-validation-${Date.now()}`;
    await page.locator('[data-testid="backlog-title-input"]').fill(title);
    await page.locator('[data-testid="backlog-description-input"]').fill(
      'Validate the triage pipeline fix: Claude should receive a prompt and submit results.'
    );
    const repoInput = page.locator('[data-testid="backlog-repo-path-input"]');
    await repoInput.fill(REPO_PATH);
    await repoInput.press('Escape'); // close autocomplete dropdown if open
    await page.waitForTimeout(300);

    await page.locator('[data-testid="backlog-form-submit"]').click();
    // Log any validation errors that prevent close.
    const repoErr = page.locator('[id="backlog-repo-path-error"]');
    const titleErr = page.locator('[id="backlog-title-error"]');
    if (await repoErr.isVisible({ timeout: 1_000 }).catch(() => false)) {
      throw new Error(`Repo path validation error: ${await repoErr.textContent()}`);
    }
    if (await titleErr.isVisible({ timeout: 500 }).catch(() => false)) {
      throw new Error(`Title validation error: ${await titleErr.textContent()}`);
    }
    // CreateBacklogItem triggers triage synchronously (30s server-side timeout) — allow extra time.
    await page.waitForSelector('[data-testid="backlog-form-modal"]', { state: 'hidden', timeout: 40_000 });

    // --- 2. Open item detail ---
    const row = page.locator('[data-testid="backlog-table-row"]').filter({ hasText: title });
    await expect(row.first()).toBeVisible({ timeout: 10_000 });
    await row.first().click();
    await page.waitForSelector('[data-testid="backlog-item-detail"]', { timeout: 5_000 });

    // --- 3. Trigger triage (or confirm already running from CreateBacklogItem auto-trigger) ---
    // CreateBacklogItem auto-triggers triage when skipTriage=false, so by the time we open
    // the detail pane triage may already be running and its terminal content may overlay the button.
    // Use force:true so the click fires regardless of overlapping elements; the server returns
    // CodeAlreadyExists if triage is already running, which the test ignores — we only care
    // that triage is running (checked in step 4).
    const triggerBtn = page.locator('[data-testid="backlog-action-trigger-triage"]');
    const alreadyRunning = await page.locator('[data-testid="backlog-item-detail"]')
      .locator('[aria-label*="triage"], [aria-label*="Cancel triage"], [class*="loading"], [class*="spinner"]')
      .first().isVisible().catch(() => false);
    if (!alreadyRunning && await triggerBtn.isVisible({ timeout: 3_000 }).catch(() => false)) {
      await triggerBtn.click({ force: true });
    }

    // --- 4. Confirm loading indicator appears (session started and prompt injected) ---
    // triageStatus === "running" renders a loading indicator in the detail pane.
    await expect(
      page.locator('[data-testid="backlog-item-detail"]').locator('[aria-label*="triage"], [aria-label*="Cancel triage"], [class*="loading"], [class*="spinner"]').first()
    ).toBeVisible({ timeout: 30_000 }).catch(async () => {
      // Fallback: check that trigger button changed state (disabled/hidden means triage started)
      await expect(triggerBtn).not.toBeVisible({ timeout: 5_000 });
    });
    console.log('✅ Triage session started (loading indicator visible or trigger button hidden)');

    // --- 5. Wait for triage-review-panel (triage submitted results, endedAt set) ---
    const reviewPanel = page.locator('[data-testid="triage-review-panel"]');
    await expect(reviewPanel).toBeVisible({ timeout: 900_000 }); // up to 15 min
    console.log('✅ Triage review panel appeared — session completed with results');

    // --- 6. Review panel must contain actual summary text ---
    const summaryText = await reviewPanel.locator('p').first().textContent();
    expect(summaryText).toBeTruthy();
    expect((summaryText ?? '').length).toBeGreaterThan(10);
    console.log(`✅ Summary: "${summaryText?.slice(0, 120)}"`);
  });
});
