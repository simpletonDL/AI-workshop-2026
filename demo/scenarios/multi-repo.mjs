// Repository list: add two repositories on the main page, see one merged skill
// list, carry the list over to /cluster, remove and re-add a row, cluster.
// Needs a fake Claude: scripts/demo-record.sh multi-repo -- --claude-bin <script>.
export default async (demo) => {
  const { page } = demo;
  const row = (n, field) => `.repo-row:nth-child(${n}) input[name=${field}]`;
  await demo.goto('/');
  await demo.pause();

  await demo.type(row(1, 'repo'), 'https://github.com/leandronsp/curupira');
  await demo.click('button[name=add]');
  await demo.type(row(2, 'repo'), 'https://github.com/yaralahruthik/find-me-a-job');
  await demo.type(row(2, 'ref'), 'main');
  await demo.click('button.primary');
  await page.waitForSelector('.summary', { timeout: 60_000 });
  await demo.pause(1500);
  await demo.screenshot('merged');
  await demo.scroll(400);
  await demo.pause(800);
  await demo.scroll(-400);

  await demo.click('.subtitle a');
  await page.waitForSelector('.repo-row:nth-child(2)');
  await demo.pause(1000);
  await demo.click('.repo-row:nth-child(2) button[name=remove]');
  await demo.pause(800);
  if (await page.locator('.repo-row').count() !== 1) throw new Error('row was not removed');
  await demo.click('button[name=add]');
  await demo.type(row(2, 'repo'), 'https://github.com/yaralahruthik/find-me-a-job');
  await demo.screenshot('cluster-form');
  await demo.click('button.primary');
  await page.waitForSelector('.cluster', { timeout: 60_000 });
  await demo.pause(1500);
  await demo.screenshot('clusters');
  await demo.scroll(400);
  await demo.pause(1000);
};
