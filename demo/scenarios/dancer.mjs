// Background dancer: walks, dances, points and talks on both pages.
export default async (demo) => {
  await demo.goto('/');
  for (let i = 1; i <= 6; i++) {
    await demo.pause(1500);
    await demo.screenshot(`main-${i}`);
  }
  await demo.type('input[name=repo]', 'https://github.com/anthropics/skills');
  await demo.click('button.primary');
  await demo.page.waitForSelector('.summary', { timeout: 60_000 });
  await demo.pause(5000);
  await demo.screenshot('results');
};
