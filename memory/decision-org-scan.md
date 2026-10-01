---
type: decision
date: 2026-10-01
description: Org scan (issue #18) = GitHub API listing (1 call/100 repos, ETag 304s) + sparse clones; main page only
---
- `/users/<name>/repos` works for users and orgs alike (public repos), `sort=pushed`, cap `--org-max-repos` 100.
- Listing cached for `--cache-ttl`, then revalidated by ETag (304 is free); stale listing on API errors.
- Empty repos (`size` 0) are not cloned; 16 parallel clones per request (8 → 16 halved anthropics: 8.6 s → 4.7 s,
  no clone failures); `--cache-size` default 50 → 500, otherwise a 100-repo org thrashes the LRU.
- anthropics live: 100 repos listed (truncated), 721 skills in 23 repos, ~5 s cold, ~0.15 s warm, 63 MB cache.
- `--github-api` exists for tests (UI harness serves a fake API at `/api`) and GitHub Enterprise.

**Why:** the issue asked to avoid running out of limits and to make it fast.
**How to apply:** `/cluster` has no org field (not asked); add it via `listOrg` if requested.
