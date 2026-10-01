// Organization scan: type a GitHub organization, watch the listing and clone
// progress, browse the merged skills of its repositories, filter them.
export default async (demo) => {
  const { page } = demo;
  await demo.goto('/');
  await demo.pause();

  await demo.type('input[name=org]', 'https://github.com/anthropics');
  await demo.click('button.primary');
  await page.waitForSelector('.progress', { timeout: 10_000 });
  await demo.pause(1500);
  await demo.screenshot('progress');
  await page.waitForSelector('.summary', { timeout: 120_000 });
  await demo.pause(1500);
  await demo.screenshot('results');
  await demo.scroll(500);
  await demo.pause(800);
  await demo.scroll(-500);

  await demo.type('input[name=filter]', 'pdf');
  await demo.click('button.primary');
  await page.waitForSelector('.summary >> text=match', { timeout: 60_000 });
  await demo.pause(1500);
  await demo.screenshot('filtered');
};
