// /cluster: the repository list carried over from the main page, clusters from the fake Claude.
import { test, expect, repo } from './harness.mjs';

const shot = (page, name) => expect(page).toHaveScreenshot(`${name}.png`, { fullPage: true });

test('cluster the repositories of the main page', async ({ page }) => {
  await page.goto('/?' + new URLSearchParams([['repo', repo('alpha')], ['repo', repo('beta')]]));
  await expect(page.locator('.summary')).toHaveText('6 skills found in 2 repositories');

  await page.click('.subtitle a');
  await expect(page.locator('.repo-row')).toHaveCount(2);
  await shot(page, 'form');

  await page.click('button.primary');
  await expect(page.locator('.cluster')).toHaveCount(4);
  await shot(page, 'clusters');
});
