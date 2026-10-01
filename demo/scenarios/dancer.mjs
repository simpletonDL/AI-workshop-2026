// Background dancer: walks, squats, dances, points and talks on both pages.
// Math.random is seeded so the run always shows every mode: squats from 2.5 s,
// the rally dance from 7.2 s, pointing from 12.3 s.
export default async (demo) => {
  await demo.page.addInitScript(() => {
    let seed = 19; // mulberry32
    Math.random = () => {
      seed = (seed + 0x6d2b79f5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  });
  await demo.goto('/');
  const t0 = Date.now();
  for (const [at, label] of [[1500, 'walk'], [3300, 'squat-down'], [4200, 'squat-up'], [5000, 'squat-down-2'],
    [8500, 'dance'], [13000, 'point'], [16000, 'walk-2']]) {
    await demo.pause(Math.max(0, at - (Date.now() - t0)));
    await demo.screenshot(label);
  }
  await demo.type('input[name=repo]', 'https://github.com/anthropics/skills');
  await demo.click('button.primary');
  await demo.page.waitForSelector('.summary', { timeout: 60_000 });
  await demo.pause(5000);
  await demo.screenshot('results');
};
