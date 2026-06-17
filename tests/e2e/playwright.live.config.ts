// Config for running tests against an already-running live server (no test server setup).
// Usage: TEST_SERVER_URL=http://localhost:8543 npx playwright test --config playwright.live.config.ts <spec>
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './',
  timeout: 420_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: process.env.TEST_SERVER_URL || 'http://localhost:8543',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    actionTimeout: 15_000,
    navigationTimeout: 20_000,
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: { args: ['--use-gl=swiftshader', '--disable-gpu-sandbox'] },
      },
    },
  ],
});
