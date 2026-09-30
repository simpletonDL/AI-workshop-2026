# Core rule
1. **Read the spec first.**
2. **Always check the tests.** Run the tests after making changes. If the tests are red, the task is **not considered done**.
3. **Every task is done in its own branch and ends with a PR.** A task is done only when CI is green for the pushed commit and the PR is open.

# Git workflow
1. **At the start of the task**, before any changes, create a new branch from an up-to-date `main`:
   ```
   git checkout main && git pull --ff-only
   git checkout -b <short-kebab-case-name>
   ```
   Never commit directly to `main`.
2. Do the work. Commit to the branch as you go.

# CI workflow
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
5. Only report the task as done after CI is green and the PR is open. In the final message, mention the commit SHA, the CI result and the PR URL.

Do not poll CI manually with repeated `gh run list` calls — use the script.
