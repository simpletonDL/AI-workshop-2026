---
type: idea
date: 2026-09-30
description: Designed + implemented once but LOST (never committed): read github.com repos via Trees API instead of cloning
---
Current code (`internal/cli/git.go`) does a shallow `git clone --depth 1`. In an early session a faster
path was built and tested on live repos, but never committed; the session ended and the files were gone
(an empty `internal/github/` dir is the only trace).

The design, if the user wants it back:
- github.com: ref → commit SHA; `GET /repos/{o}/{r}/git/trees/{sha}?recursive=1` (whole tree, one call);
  fetch each `SKILL.md` from `raw.githubusercontent.com/{o}/{r}/{sha}/{path}` in parallel (raw doesn't
  count against the REST limit). Send `GITHUB_TOKEN`/`GH_TOKEN` if set.
- Fallback to a sparse clone of only `SKILL.md`: other hosts, GHE, local dir with `--ref`, and on API
  failure (60 req/h without a token, 404 on private repos, `truncated: true`, network) with a stderr warning.

**How to apply:** don't claim atlas uses the GitHub API. Lesson: uncommitted work is lost when a session ends — if the user says "пока не коммить", remind them before finishing.
