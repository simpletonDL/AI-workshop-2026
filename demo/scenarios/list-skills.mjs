// Main page: scan a repository with progress, open a skill, filter by name.
export default async (demo) => {
  const { page } = demo;
  await demo.goto('/');
  await demo.pause();

  await demo.type('input[name=repo]', 'https://github.com/anthropics/skills');
  await demo.click('button[type=submit]');
  await page.waitForSelector('.progress');
  await demo.screenshot('progress');
  await page.waitForSelector('.summary', { timeout: 60_000 });
  await demo.pause(1200);
  await demo.screenshot('results');

  await demo.click('details.skill summary');
  await demo.pause(1500);
  await demo.scroll(300);
  await demo.pause(800);
  await demo.scroll(-300);

  await demo.type('input[name=filter]', 'doc');
  await demo.click('button[type=submit]');
  await page.waitForSelector('.summary:has-text("match")', { timeout: 60_000 });
  await demo.pause(1500);
};
