// Background dancer on both pages.
export default async (demo) => {
  await demo.goto('/');
  await demo.pause(2500);
  await demo.screenshot('main');
  await demo.type('input[name=repo]', 'https://github.com/anthropics/skills');
  await demo.click('button.primary');
  await demo.page.waitForSelector('.summary', { timeout: 60_000 });
  await demo.pause(2500);
  await demo.screenshot('results');
};
