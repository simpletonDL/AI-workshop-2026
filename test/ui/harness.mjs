// Test fixtures of the UI tests: a local git server with the fixture repositories
// and a fresh `atlas serve` per test, so every run sees exactly the same data.
import { test as base, expect } from '@playwright/test';
import { execFileSync, spawn } from 'node:child_process';
import { cpSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const here = import.meta.dirname;
const atlasBin = resolve(process.env.ATLAS_BIN || join(here, '../../bin/atlas'));
const fakeClaude = join(here, 'fixtures/fake-claude.sh');

// A fixed address: repository URLs are part of the screenshots.
export const gitPort = 47123;
export const repo = (name) => `http://127.0.0.1:${gitPort}/${name}.git`;

// Fixed commit identity and dates: the fixture repositories are the same on every run.
const gitEnv = {
  ...process.env,
  GIT_AUTHOR_NAME: 'atlas', GIT_AUTHOR_EMAIL: 'atlas@example.com', GIT_AUTHOR_DATE: '2026-01-01T00:00:00Z',
  GIT_COMMITTER_NAME: 'atlas', GIT_COMMITTER_EMAIL: 'atlas@example.com', GIT_COMMITTER_DATE: '2026-01-01T00:00:00Z',
};

// Turns every fixtures/repos/<name> into a bare repository <root>/<name>.git.
function makeRepos(root) {
  const src = join(here, 'fixtures/repos');
  for (const name of readdirSync(src)) {
    const work = join(root, 'work', name);
    cpSync(join(src, name), work, { recursive: true });
    const git = (...args) => execFileSync('git', args, { cwd: work, env: gitEnv, stdio: 'pipe' });
    git('init', '-q', '-b', 'main');
    git('add', '-A');
    git('commit', '-q', '-m', 'fixture');
    execFileSync('git', ['clone', '-q', '--bare', work, join(root, `${name}.git`)], { stdio: 'pipe' });
  }
}

// A fake GitHub API on the same server (`--github-api <git server>/api`): the
// organization "fixtures" owns alpha, beta and an empty repository.
const fakeOrgs = {
  fixtures: [
    { html_url: repo('alpha'), size: 10 },
    { html_url: repo('beta'), size: 10 },
    { html_url: repo('empty'), size: 0 },
  ],
};

function serveGitHubAPI(url, res) {
  const org = url.pathname.match(/^\/api\/users\/([^/]+)\/repos$/)?.[1];
  res.setHeader('Content-Type', 'application/json');
  if (!fakeOrgs[org]) return void res.writeHead(404).end('{"message":"Not Found"}');
  res.end(JSON.stringify(fakeOrgs[org]));
}

// Smart HTTP git server (git http-backend as CGI) plus the fake GitHub API.
// hold(name) keeps the requests for that repository (or "api") waiting until
// the returned function (or releaseAll) is called.
async function startGitServer() {
  const root = mkdtempSync(join(tmpdir(), 'atlas-ui-git-'));
  makeRepos(root);
  const gates = new Map();
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://git');
    const name = url.pathname.split('/')[1]?.replace(/\.git$/, '');
    await gates.get(name)?.promise;
    if (name === 'api') return serveGitHubAPI(url, res);
    const cgi = spawn('git', ['http-backend'], {
      env: {
        ...process.env,
        GIT_PROJECT_ROOT: root, GIT_HTTP_EXPORT_ALL: '1',
        PATH_INFO: decodeURIComponent(url.pathname), QUERY_STRING: url.search.slice(1),
        REQUEST_METHOD: req.method, CONTENT_TYPE: req.headers['content-type'] || '',
        HTTP_CONTENT_ENCODING: req.headers['content-encoding'] || '',
        GIT_PROTOCOL: req.headers['git-protocol'] || '', REMOTE_ADDR: '127.0.0.1',
      },
    });
    req.pipe(cgi.stdin);
    let head = Buffer.alloc(0);
    let sent = false;
    cgi.stdout.on('data', (chunk) => {
      if (sent) return void res.write(chunk);
      head = Buffer.concat([head, chunk]);
      const end = head.indexOf('\r\n\r\n');
      if (end < 0) return;
      let status = 200;
      for (const line of head.subarray(0, end).toString().split('\r\n')) {
        const i = line.indexOf(':');
        const [key, value] = [line.slice(0, i), line.slice(i + 1).trim()];
        if (key.toLowerCase() === 'status') status = parseInt(value, 10);
        else res.setHeader(key, value);
      }
      res.writeHead(status);
      res.write(head.subarray(end + 4));
      sent = true;
    });
    cgi.on('close', () => res.end());
  });
  await new Promise((ok, fail) => server.once('error', fail).listen(gitPort, '127.0.0.1', ok));
  return {
    hold(name) {
      let release;
      const promise = new Promise((ok) => { release = ok; });
      gates.set(name, { promise, release });
      return () => { gates.delete(name); release(); };
    },
    releaseAll() {
      for (const [name, gate] of gates) { gates.delete(name); gate.release(); }
    },
    async close() {
      await new Promise((ok) => server.close(ok));
      rmSync(root, { recursive: true, force: true });
    },
  };
}

// Starts `atlas serve` on a free port with an empty cache and history.
async function startAtlas() {
  const cache = mkdtempSync(join(tmpdir(), 'atlas-ui-cache-'));
  const proc = spawn(atlasBin, ['serve', '--addr', '127.0.0.1:0', '--cache-dir', cache, '--claude-bin', fakeClaude,
    '--github-api', `http://127.0.0.1:${gitPort}/api`],
    { stdio: ['ignore', 'pipe', 'pipe'] });
  let log = '';
  proc.stderr.on('data', (d) => { log += d; });
  const url = await new Promise((ok, fail) => {
    let out = '';
    proc.stdout.on('data', (d) => {
      out += d;
      const m = out.match(/Serving atlas on (http\S+)/);
      if (m) ok(m[1]);
    });
    proc.once('exit', (code) => fail(new Error(`atlas serve exited with ${code}:\n${log}`)));
    proc.once('error', fail);
  });
  return {
    url,
    async close() {
      proc.kill('SIGTERM');
      await new Promise((ok) => (proc.exitCode === null ? proc.once('exit', ok) : ok()));
      rmSync(cache, { recursive: true, force: true });
    },
  };
}

export const test = base.extend({
  git: [async ({}, use) => {
    const git = await startGitServer();
    await use(git);
    await git.close();
  }, { scope: 'worker' }],

  baseURL: async ({ git }, use) => {
    const atlas = await startAtlas();
    await use(atlas.url);
    git.releaseAll(); // a failed test may leave a clone waiting
    await atlas.close();
  },
});

export { expect };
