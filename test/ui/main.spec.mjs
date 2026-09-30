// Main page: scan with progress, skill text, filter, several repositories, errors, history.
import { test, expect, repo } from './harness.mjs';

const shot = (page, name) => expect(page).toHaveScreenshot(`${name}.png`, { fullPage: true });

test('empty form', async ({ page }) => {
  await page.goto('/');
  await shot(page, 'empty');
});

test('scan one repository: progress, results, skill text', async ({ page, git }) => {
  await page.goto('/');
  await page.fill('input[name=repo]', repo('slow'));
  // The clone waits for the git server, so the progress bar stays at a known stage.
  const release = git.hold('slow');
  await page.click('button.primary');
  await expect(page.locator('.progress-stage')).toHaveText(`Cloning ${repo('slow')}…`);
  await expect(page.locator('.progress-percent')).toHaveText('0%');
  await shot(page, 'progress');
  release();

  await expect(page.locator('.summary')).toHaveText('1 skill found');
  await shot(page, 'one-skill');
});

test('results, expanded skill and filter', async ({ page }) => {
  await page.goto('/?' + new URLSearchParams({ repo: repo('alpha') }));
  await expect(page.locator('.summary')).toHaveText('4 skills found');
  await shot(page, 'results');

  await page.click('details.skill:has(h2:text-is("code-review")) summary');
  await expect(page.locator('details.skill[open] .skill-text')).toBeVisible();
  await shot(page, 'skill-text');

  await page.fill('input[name=filter]', 'doc');
  await page.click('button.primary');
  await expect(page.locator('.summary')).toContainText('1 of 4 skills matches "doc"');
  await shot(page, 'filtered');

  await page.fill('input[name=filter]', 'xyz');
  await page.click('button.primary');
  await expect(page.locator('.message.empty')).toContainText('No skills match "xyz"');
  await shot(page, 'no-match');
});

test('several repositories with errors, then history', async ({ page }) => {
  await page.goto('/');
  const row = (n, field) => page.locator(`.repo-row:nth-child(${n}) input[name=${field}]`);
  await row(1, 'repo').fill(repo('alpha'));
  await page.click('button[name=add]');
  await row(2, 'repo').fill(repo('beta'));
  await row(2, 'ref').fill('main');
  await page.click('button[name=add]');
  await row(3, 'repo').fill(repo('missing'));
  await page.click('button[name=add]');
  await row(4, 'repo').fill('/etc');
  await shot(page, 'repo-list');

  await page.click('button.primary');
  await expect(page.locator('.summary')).toHaveText('6 skills found in 4 repositories');
  await shot(page, 'merged-with-errors');

  // Only fully successful searches are recorded.
  await page.goto('/?' + new URLSearchParams([['repo', repo('beta')]]));
  await page.goto('/?' + new URLSearchParams([['repo', repo('alpha')]]));
  await page.goto('/');
  await expect(page.locator('nav.history li')).toHaveCount(2);
  await shot(page, 'history');
});

test('clone failure', async ({ page }) => {
  await page.goto('/?' + new URLSearchParams({ repo: repo('missing') }));
  await expect(page.locator('.message.error')).toContainText('failed to clone');
  await shot(page, 'clone-failure');
});
