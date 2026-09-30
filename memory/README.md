# Shared memory
Notes that outlive a single session: decisions, gotchas, user preferences, history.
Shared by all agents (the main session and sandboxed task agents) through git.

## Format
One note per file: `memory/<type>-<slug>.md`. One file per note means parallel branches don't conflict.

```markdown
---
type: decision | gotcha | preference | idea | history
date: YYYY-MM-DD          # when it was learned / last confirmed
description: one line — used to decide relevance without opening the file
---
The fact. For decision/preference: **Why:** and **How to apply:** lines.
Link related notes by file name: [[gotcha-sbx-no-tty]].
```

- **decision** — why something is built the way it is (not what — the code/spec says that).
- **gotcha** — a trap that cost time: symptom, cause, workaround.
- **preference** — how the user wants the work done.
- **idea** — designed or discussed, not in the code (yet).
- **history** — what was done when, PR numbers; one file per day.

## Usage
- **Recall:** `grep -H '^description:' memory/*.md` — then open only the relevant notes.
- **Write:** only non-obvious things that are not already in the code, spec, `CLAUDE.md` or git history.
  Update an existing note rather than adding a duplicate; delete notes that became wrong.
- Keep notes short, in English.
