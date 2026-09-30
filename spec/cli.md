# atlas CLI

## Description
`atlas` is a Go CLI for inspecting skills (Claude Code skills) in a git repository.

## Commands

### `atlas list-skills <repo-url>`
Clones the repository (shallow, into a temp directory), finds all skills and prints them as a list.

- **Input:** git repository URL (https or ssh)
- **Skill:** any `SKILL.md` file in the repository
- **Skill data:** taken from the `SKILL.md` YAML frontmatter:
  - `name` — skill name (falls back to the skill directory name);
  - `description` — short description.
- **Output:** a table sorted by name:
  ```
  NAME            DESCRIPTION
  code-review     Review the current diff for correctness bugs
  deploy          Deploy the service to staging
  ```
- **Flags:**
  - `--ref <branch|tag>` — branch/tag to use (defaults to the default branch);
  - `--json` — JSON output: `[{"name", "description", "path"}]`.
- **Errors:** invalid URL / clone failure — message to stderr, non-zero exit code. No skills found — prints `No skills found`, exit code 0.

### `atlas serve`
Starts a local web service with a simple browser UI for viewing a repository's skills.

- **UI:** a page with an input for the repository URL (and an optional ref). On submit it shows the same results as `list-skills` (name, description, path), rendered as a clean, readable table or card list. The form uses `GET /?repo=<url>&ref=<ref>`, so result pages can be bookmarked and shared.
- **Filter by name:** the form has an optional "filter" input; the query becomes `GET /?repo=<url>&ref=<ref>&filter=<text>`, so filtered pages can be bookmarked and shared too. Filtering happens on the server (works without JavaScript) and is web UI only — `list-skills` has no filter. Rules:
  - case-insensitive substring match on the skill `name` only (not the description or path); the filter is trimmed, and an empty filter shows all skills;
  - with an active filter the page shows a count such as `3 of 12 skills match "code"`, or `No skills match "xyz"` when nothing matches (distinct from "No skills found" for a repository without skills), plus a "Clear filter" link to the same repo/ref without the filter;
  - the filter text is HTML-escaped when echoed back;
  - the filter does not affect recent searches: entries stay keyed by repo + ref and record the total (unfiltered) number of skills.
- **Skill text:** each skill card can be expanded by clicking it to show the full raw text of its `SKILL.md` (frontmatter included) in a monospace block; cards are collapsed by default. Works without JavaScript. Files larger than 256 KiB are truncated with a visible "truncated" note. The text is HTML-escaped.
- **Recent searches:** the page shows a list of recent successful searches (repo, ref if set, number of skills found), most recent first, each a link to its result page (`/?repo=…&ref=…`). Rules:
  - only searches that completed without an error are recorded (including "no skills found");
  - repeating a search moves it to the top instead of adding a duplicate (same repo + ref);
  - at most 10 entries are kept;
  - history lives in the server's memory: it is shared by all visitors and lost on restart.
- **Input:** only remote repository URLs (`https://`, `http://`, `ssh://`, `git://`, `user@host:path`). Unlike the CLI, local paths, `file://` and other transports are rejected, so visitors cannot scan the server's filesystem.
- **Errors:** clone failures and "no skills found" are shown as messages in the UI.
- **Skill clustering (`/cluster`):** a separate page (linked from the main page and back) that groups the skills of several repositories by meaning using Claude.
  - **Form:** a textarea with repositories, one per line: `<url>` or `<url> <ref>`. It is submitted as `GET /cluster?repos=<textarea contents>`, so results can be bookmarked. Works without JavaScript.
  - **Input:** blank lines are skipped, duplicate lines (same URL + ref) are dropped, at most 10 repositories per request (more → error, nothing is cloned). Each URL goes through the same remote-only validation as the main page.
  - **Scanning:** repositories are cloned in parallel (same shallow clone as the main page) and their skills are discovered. Per-repository errors (invalid URL or line, clone failure) are listed in the UI and don't stop the other repositories.
  - **Clustering:** every skill gets an id (`s1`, `s2`, … in repository order, then by name). Only the ids, names and descriptions (truncated to 500 characters) are sent to Claude — no URLs, paths or file contents. Atlas runs `<claude-bin> -p --output-format json` with the prompt on stdin (in the system temp directory), takes the `result` field of the JSON envelope (output that is not an envelope is used as is) and parses the model answer as a JSON array `[{"name", "description", "skills": [<ids>]}]`; the outermost `[...]` is extracted, so markdown fences or surrounding prose are tolerated.
  - **Validation of the answer:** unknown or non-string ids are ignored; a skill listed in several clusters stays in the first one; clusters left without skills are dropped; a cluster without a name is called "Unnamed cluster"; skills the model left out go into an extra "Other" cluster. So every skill appears in exactly one cluster.
  - **Output:** cluster cards (name, description, number of skills) with their skills (name, description, repository + ref, path), plus the list of scanned repositories with skill counts.
  - **Errors (shown in the UI):** `claude` binary not found, non-zero exit, timeout, an error envelope (`is_error`), invalid JSON in the answer. If no skills are found, Claude is not called and "No skills found" is shown.
  - **Dependency:** requires [Claude Code](https://claude.com/claude-code) (`claude` CLI) installed and authenticated on the server. The rest of the UI works without it.
- **Progress:** while a scan (main page) or clustering (`/cluster`) runs, the page shows the current stage and a percentage with a progress bar. This is progressive enhancement: without JavaScript the forms submit normally and the final page is shown as before.
  - With JavaScript (`/progress.js`, included by both pages) the form submit is intercepted: the script generates a random job id, fetches the same bookmarkable `GET` URL with the header `X-Atlas-Progress: <id>` and polls `GET /progress?id=<id>` (every ~0.5 s) until the page arrives; then it replaces the document with the result and puts the URL into the address bar (`history.pushState`), so results stay bookmarkable and HTTP statuses are unchanged.
  - `GET /progress?id=<id>` returns `{"stage": "<text>", "percent": <0-100>, "done": <bool>}` (`Cache-Control: no-store`), or `404` for an unknown id. Ids are 16–64 characters `[A-Za-z0-9_-]`; an invalid or already running id, or more than 100 jobs at once, just disables progress for that request. A finished job stays visible for 30 seconds, then it is forgotten. Progress lives in the server's memory.
  - Stages of the main page: `Validating…`, `Cloning <repo>…`, `Discovering skills…`, `Reading N SKILL.md files…`, `Done`. Stages of `/cluster`: `Validating…`, `Cloning N repositories…`, `Cloned <repo> (k/N), discovering skills…`, `Scanned <repo> (k/N)…`, `Clustering M skills with Claude…`, `Done`. Repository URLs in stages have credentials (user info) removed.
  - Percent is computed from weighted steps: main page — cloning 80%, discovery 10%, reading SKILL.md files 10%; `/cluster` — cloning and discovery are one step each per repository (a failed repository still counts its steps) and together make the first 50%, the Claude call is the other 50%. Percent never goes down, stays at most 99% while work is running and becomes 100% when the request completes (also on errors).
- **Logging:** the server logs to stderr with `log/slog` (text format, level INFO); the `Serving atlas on …` line stays on stdout.
  - Every request: `request started` (method, path) and `request finished` (method, path, status, duration), with a request number `req` shared by all lines of that request. Only the path is logged, not the query. `/progress` and `/progress.js` requests are logged at DEBUG level (hidden by default) so polling doesn't flood the log.
  - Stages of work: main page — `scan: validating`, `scan: cloning`, `scan: clone done` / `scan: clone failed` (duration, error), `scan: discovering skills`, `scan: skills found` (count), `scan: done`; `/cluster` — `cluster: validating`, `cluster: repositories parsed` (valid/invalid counts), per repository `cluster: cloning`, `cluster: clone done` / `cluster: clone failed` (duration), `cluster: discovering skills`, `cluster: skills found`, then `cluster: calling Claude` (number of skills), `cluster: Claude done` (duration, number of clusters) or `cluster: Claude failed` / `cluster: invalid Claude answer` (duration, error).
  - No secrets or contents are logged: repository URLs (also inside error messages) have user info removed, and neither `SKILL.md` texts nor the Claude prompt/answer are logged.
- **Flags:**
  - `--addr <host:port>` — listen address (defaults to `localhost:8080`);
  - `--claude-bin <path>` — Claude Code CLI used by `/cluster` (defaults to `claude`, looked up in `PATH`);
  - `--claude-timeout <duration>` — timeout of one Claude call (defaults to `3m`).

## Technical requirements
- Go 1.22+, CLI built with [cobra](https://github.com/spf13/cobra).
- Cloning via system `git` (or `go-git`).
- Frontmatter parsing with `gopkg.in/yaml.v3`.

- Layout:
  ```
  cmd/atlas/main.go        # entry point
  internal/cli/            # cobra commands (root, list-skills, serve + embedded HTML pages and progress script)
  internal/skills/         # SKILL.md discovery and parsing
  test/integration/        # integration tests
  ```
- Unit tests for skill discovery and parsing, and for the web UI handler (skill text, recent searches, name filter, clustering with a fake Claude runner — unit tests never call the real Claude, progress tracking and the `/progress` endpoint, logging).
- Integration tests (build tag `integration`, `make test-integration`) check the whole pipeline: they build the `atlas` binary and run it against small real public GitHub repositories (clone → discovery → parsing → output). The `/cluster` integration tests pass a fake `claude` script via `--claude-bin` that prints a canned JSON envelope, so CI needs neither Claude nor an API key. Both unit and integration tests run in CI (GitHub Actions).

## Installation
- `go install github.com/<org>/atlas/cmd/atlas@latest`
- `scripts/install.sh` — installs from a local clone (`go install ./cmd/atlas`), checks that Go is installed and that `$(go env GOPATH)/bin` is in `PATH`.
- `Makefile`: `build`, `install`, `test`, `test-integration`.
