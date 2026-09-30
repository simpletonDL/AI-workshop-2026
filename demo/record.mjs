// Records one demo scenario of a running atlas serve into a video and a GIF.
// Usage: node record.mjs <scenario> <base-url> <out-dir>
// A scenario is demo/scenarios/<name>.mjs: export default async (demo) => {...}.
import { chromium } from 'playwright';
import ffmpeg from 'ffmpeg-static';
import { spawnSync } from 'node:child_process';
import { mkdirSync, renameSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [name, baseURL, outArg] = process.argv.slice(2);
if (!name || !baseURL || !outArg) {
  console.error('usage: node record.mjs <scenario> <base-url> <out-dir>');
  process.exit(2);
}
const out = resolve(outArg);
const size = { width: 1280, height: 800 };
const scenario = (await import(`./scenarios/${name}.mjs`)).default;

mkdirSync(out, { recursive: true });
const rawDir = join(out, 'raw');
const browser = await chromium.launch();
const context = await browser.newContext({ viewport: size, recordVideo: { dir: rawDir, size } });
const page = await context.newPage();
let shots = 0;
let cursor = { x: size.width / 2, y: size.height / 2 };

// Headless video has no mouse pointer: draw one. Pages replaced by
// document.write lose it, so it is re-added before every move.
async function showCursor() {
  await page.evaluate(({ x, y }) => {
    if (document.getElementById('demo-cursor')) return;
    const c = document.createElement('div');
    c.id = 'demo-cursor';
    c.style.cssText = 'position:fixed;z-index:2147483647;pointer-events:none;width:18px;height:18px;' +
      'margin:-9px 0 0 -9px;border-radius:50%;background:rgba(255,80,80,.55);border:2px solid #fff;' +
      'box-shadow:0 0 4px rgba(0,0,0,.5);transition:transform .15s';
    c.style.left = x + 'px';
    c.style.top = y + 'px';
    document.documentElement.appendChild(c);
    addEventListener('mousemove', e => { c.style.left = e.clientX + 'px'; c.style.top = e.clientY + 'px'; }, true);
    addEventListener('mousedown', () => { c.style.transform = 'scale(.7)'; }, true);
    addEventListener('mouseup', () => { c.style.transform = ''; }, true);
  }, cursor);
}

const demo = {
  page,
  baseURL,
  pause: (ms = 800) => page.waitForTimeout(ms),

  async goto(path = '/') {
    await page.goto(new URL(path, baseURL).href);
    await showCursor();
  },

  // Glides the cursor to the element center.
  async moveTo(selector) {
    const el = page.locator(selector).first();
    await el.scrollIntoViewIfNeeded();
    const box = await el.boundingBox();
    if (!box) throw new Error(`not visible: ${selector}`);
    const to = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    await showCursor();
    await page.mouse.move(to.x, to.y, { steps: 25 });
    cursor = to;
    return el;
  },

  async click(selector) {
    const el = await demo.moveTo(selector);
    await demo.pause(250);
    await page.mouse.down();
    await page.mouse.up();
    await demo.pause(300);
    return el;
  },

  // Clicks the field, clears it and types the text key by key, like a person.
  async type(selector, text, { delay = 70 } = {}) {
    const el = await demo.click(selector);
    await el.fill('');
    await el.pressSequentially(text, { delay });
    await demo.pause(400);
  },

  // Smooth page scroll by dy pixels.
  async scroll(dy, { steps = 20 } = {}) {
    for (let i = 0; i < steps; i++) {
      await page.mouse.wheel(0, dy / steps);
      await page.waitForTimeout(25);
    }
    await demo.pause(300);
  },

  // Saves a PNG to out-dir: use these to check what the video shows.
  async screenshot(label = `step-${++shots}`) {
    await page.screenshot({ path: join(out, `${name}-${label}.png`) });
  },
};

let failed;
try {
  await scenario(demo);
  await demo.pause(1500); // linger on the final state
} catch (err) {
  failed = err;
  await page.screenshot({ path: join(out, `${name}-error.png`) }).catch(() => {});
}
await demo.screenshot('final').catch(() => {});
const video = page.video();
await context.close();
await browser.close();

const webm = join(out, `${name}.webm`);
renameSync(await video.path(), webm);
rmSync(rawDir, { recursive: true, force: true });
if (failed) {
  console.error(`scenario failed: ${failed.message}\nvideo: ${webm}`);
  process.exit(1);
}

// 10 fps, 960px wide, own palette: readable and a few MB for ~20 s.
const gif = join(out, `${name}.gif`);
const r = spawnSync(ffmpeg, ['-y', '-loglevel', 'error', '-i', webm, '-vf',
  'fps=10,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5',
  gif], { stdio: 'inherit' });
if (r.status !== 0) process.exit(1);
console.log(`video: ${webm}\ngif: ${gif}`);
