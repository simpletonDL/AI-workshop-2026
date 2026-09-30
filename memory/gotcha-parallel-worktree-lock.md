---
type: gotcha
date: 2026-09-30
description: Starting several sbx-claude.sh at the same moment collides on the .git/config lock
---
Symptom: one of two agents started simultaneously exited immediately; `git worktree add -b` failed on
a lock of `.git/config` and left an empty branch behind.
Workaround: start agents a few seconds apart (or make the script wait for the lock). If it happens,
delete the empty branch and restart with the same name.
