// Explains failed UI tests: which test failed, which screenshot differs and where its
// expected/actual/diff images are. Prints that after the run and writes out/failures.json,
// which scripts/ui-report.sh turns into links, annotations and the job summary in CI.
import fs from 'node:fs';
import path from 'node:path';

const clean = s => s.replace(/\x1b\[[0-9;]*m/g, '');

export default class FailuresReporter {
  onBegin(config) {
    this.root = config.rootDir;
    this.failures = [];
  }

  onTestEnd(test, result) {
    if (test.outcome() !== 'unexpected') return;
    // An error message up to its call log, as trimmed non-empty lines.
    const errors = result.errors.map(e => clean(e.message ?? String(e.value ?? ''))
      .split('Call log:')[0].split('\n').map(l => l.trim()).filter(Boolean));

    // toHaveScreenshot attaches <name>-expected.png (the baseline itself), <name>-actual.png and <name>-diff.png.
    const snapshots = new Map();
    for (const a of result.attachments) {
      const m = a.path && a.name.match(/^(.*)-(expected|actual|diff)\.png$/);
      if (!m) continue;
      const name = `${m[1]}.png`;
      if (!snapshots.has(name)) snapshots.set(name, { name });
      snapshots.get(name)[m[2]] = path.relative(this.root, a.path);
    }
    const used = new Set();
    for (const s of snapshots.values()) {
      const i = errors.findIndex(lines => lines.includes(`Snapshot: ${s.name}`));
      if (i < 0) continue;
      used.add(i);
      s.message = errors[i].slice(1).filter(l => !l.startsWith('Snapshot:')).join(' ') || errors[i][0];
    }

    const [file, ...title] = test.titlePath().filter(Boolean);
    this.failures.push({
      file: path.relative(this.root, test.location.file),
      line: test.location.line,
      title: [file, ...title].join(' › '),
      errors: errors.filter((_, i) => !used.has(i)).map(lines => lines.slice(0, 3).join(' ')),
      snapshots: [...snapshots.values()],
    });
  }

  onEnd() {
    const out = path.join(this.root, 'out');
    fs.mkdirSync(out, { recursive: true });
    fs.writeFileSync(path.join(out, 'failures.json'), JSON.stringify(this.failures, null, 2) + '\n');
    if (!this.failures.length) return;

    const rel = p => `test/ui/${p}`;
    const lines = ['', `UI test failures (${this.failures.length}):`];
    for (const f of this.failures) {
      lines.push('', `  ✘ ${f.title}  (test/ui/${f.file}:${f.line})`);
      for (const e of f.errors) lines.push(`    ${e}`);
      for (const s of f.snapshots) {
        lines.push(`    screenshot ${s.name}${s.message ? ': ' + s.message : ''}`);
        for (const k of ['expected', 'actual', 'diff']) if (s[k]) lines.push(`      ${k.padEnd(8)} ${rel(s[k])}`);
      }
    }
    lines.push('', '  Full report: test/ui/out/report/index.html',
      '  An intended UI change? Re-render the baselines: scripts/ui-test.sh --update', '');
    console.log(lines.join('\n'));
  }
}
