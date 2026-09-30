---
type: decision
date: 2026-09-30
description: serve caches clones per URL+ref (--cache-ttl, --cache-size=50, LRU) in a per-run temp dir
---
Same repo is cloned once even under concurrent requests; a copy isn't removed while being read;
failed clones aren't cached; `--cache-ttl 0` disables the cache. `list-skills` still clones to a
temp dir and deletes it.

**Why:** repeat requests (and `/cluster` over several repos) were slow.
**How to apply:** the cache dir is deleted on normal shutdown only — a crashed server leaves it in temp.
Cache logs go through the shared request logger (from the progress/logging PR).
