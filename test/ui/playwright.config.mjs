// UI tests: deterministic scenarios in Chromium, screenshots at key moments are
// compared with the baselines in __screenshots__/. Run with scripts/ui-test.sh:
// baselines are rendered in the Playwright Docker image, so fonts match everywhere.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.mjs',
  // One worker: the fixture git server listens on a fixed port.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 60_000,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'out/report' }], ['./failures-reporter.mjs']],
  outputDir: 'out/results',
  snapshotPathTemplate: '{testDir}/__screenshots__/{testFileName}/{arg}{ext}',
  expect: {
    toHaveScreenshot: { animations: 'disabled', caret: 'hide', scale: 'css' },
  },
  use: {
    browserName: 'chromium',
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 1,
    locale: 'en-US',
    timezoneId: 'UTC',
    colorScheme: 'light',
    // The background dancer stands still; dancer.spec.mjs drives his animation with a fake clock.
    reducedMotion: 'reduce',
    trace: 'retain-on-failure',
  },
});
