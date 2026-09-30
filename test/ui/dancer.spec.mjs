// Background dancer: with a seeded Math.random and a paused fake clock his walk,
// dance and quips are the same on every run, so frames at fixed moments can be compared.
import { test, expect } from './harness.mjs';

test.use({ reducedMotion: 'no-preference' });

// Moments (ms since the page load) chosen to show every mode of the seeded run.
const moments = [0, 1000, 2400, 3000, 4500, 7000, 10000, 14000];

test('dancer timeline', async ({ page }) => {
  await page.addInitScript(() => {
    let seed = 42; // mulberry32
    Math.random = () => {
      seed = (seed + 0x6d2b79f5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  });
  await page.clock.install({ time: new Date('2026-01-01T00:00:00Z') });
  await page.clock.pauseAt(new Date('2026-01-01T00:00:01Z'));
  await page.goto('/');

  let now = 0;
  for (const at of moments) {
    await page.clock.runFor(at - now);
    now = at;
    // Soft: a changed dance shows every frame that differs, not only the first one.
    await expect.soft(page).toHaveScreenshot(`t${String(at).padStart(5, '0')}.png`);
  }
});

test('dancer stands still with reduced motion', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/cluster');
  await expect(page).toHaveScreenshot('reduced-motion.png');
});
