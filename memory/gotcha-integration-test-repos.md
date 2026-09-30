---
type: gotcha
date: 2026-09-30
description: Integration tests use small public repos chosen for corner cases (dup dirs, odd agent dirs, symlinks)
---
Build the real binary and run it on live GitHub repos (tag `integration`, `make test-integration`, ~15s):
- `yaralahruthik/find-me-a-job` — same skill in `.agents/`, `.claude/`, `.cursor/`;
- `nzrsky/zig-skills` — `skills/` plus `.agent`, `.adal`, `.pi`, `.codebuddy`, ...;
- `leandronsp/curupira` — only `.claude/skills`;
- `zacharyfmarion/openscad-studio` — `.claude/skills/*` are symlinks to `.agents/skills/*` (listed once),
  a real copy in both dirs is listed twice.
Good manual demo repo: `https://github.com/anthropics/skills` (~20 skills).
**How to apply:** these repos can change or disappear upstream — if integration tests break without a code change, check them first.
