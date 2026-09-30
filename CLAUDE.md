# Task workflow
This is the default way to work on a task. Running several tasks in parallel in sandboxes is a separate
mode — the `orchestrate` skill (`.claude/skills/orchestrate/SKILL.md`); use it only when the user asks for it.

## Does the task change code?
First decide what kind of task it is:

- **Research / questions / analysis** — read the code, specs, logs, docs, the issue tracker, and answer.
  Nothing in the repository changes. **Do not run the pipeline**: no branch, no commits, no tests, no push, no CI, no PR.
  Just give the answer.
- **Code change** — anything that edits code, specs, tests, scripts or configs in the repository.
  Run the full pipeline below.

If a research task turns out to need a code change, say so and switch to the pipeline only then.

## Pipeline (code changes only)

### Core rule
1. **Read the spec first.**
2. **Always check the tests.** Run the tests after making changes. If the tests are red, the task is **not considered done**.
3. **Every code change ends with a PR.** The task is done only when CI is green for the pushed commit and the PR is open.

### Git workflow
1. Never commit to `main`. If you are on `main` (the main repo root), create a task branch first:
   `git fetch origin && git switch -c <name> origin/main` (`<name>` — a short kebab-case task name).
   In a git worktree (`.git` is a file) you are already on the task branch, created from `origin/main` — stay on it, never switch branches.
2. Do the work. Commit to the branch as you go.

### CI workflow
After the local tests (`make test`, `make test-integration`) pass:

1. Commit the changes and push the branch: `git push -u origin HEAD` (you are allowed to do this without asking).
2. Wait for CI: run `scripts/ci-wait.sh` (Bash timeout 600000 ms, or `run_in_background` and wait for the notification).
   - It finds the GitHub Actions `Tests` run for the current `HEAD` commit and waits for it to finish.
   - Exit code 0 — CI is green. Exit code 1 — CI failed (failed job logs are printed), HEAD is not pushed, or no run was found.
3. If CI is red: read the logs, fix, commit, push, and run `scripts/ci-wait.sh` again.
4. Once `scripts/ci-wait.sh` prints `CI GREEN`, open a PR into `main` (you are allowed to do this without asking):
   ```
   gh pr create --base main --title "<title>" --body "<summary of changes>"
   ```
   If a PR for the branch already exists, just push — don't create a second one.
5. Only report the task as done after CI is green and the PR is open.

Do not poll CI manually with repeated `gh run list` calls — use the script.

## Headless run: final report
If you run headless in a sandbox (`claude -p`, started by the `orchestrate` skill via `scripts/sbx-claude.sh`),
your **last message is the only thing the orchestrator receives**. Nobody can answer questions, so don't ask any —
make a reasonable decision and mention it in the report. The last message must be exactly this format:
```
STATUS: DONE | FAILED
BRANCH: <branch>
COMMIT: <sha or none>
CI: GREEN | RED | NOT RUN
PR: <url or none>
SUMMARY: <what was done / the answer for a research task, decisions made on your own>
PROBLEMS: <why it failed / what is left; "none" if DONE>
```
For a code change, `STATUS: DONE` only when CI is green and the PR is open; otherwise `FAILED`.
For a research task (no code changes), `STATUS: DONE` with `CI: NOT RUN`, `PR: none`, and the answer in `SUMMARY`.
