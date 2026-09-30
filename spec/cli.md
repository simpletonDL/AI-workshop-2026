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
Starts a local web service with a simple browser UI for viewing the skills of one or several repositories.

- **Repository list:** both pages (main and `/cluster`) share the same form: a list of rows, each with a repository URL, an optional ref and a "×" (remove) button, plus a "+ Add repository" button.
  - The form submits the rows as repeated `repo`/`ref` pairs, paired by position: `GET /?repo=<url1>&ref=<ref1>&repo=<url2>&ref=`. A missing `ref` means the default branch. Blank rows are skipped and duplicate rows (same URL + ref) are dropped; at most 10 repositories per request (more → error, nothing is cloned). Each URL goes through the remote-only validation (see **Input**); an invalid URL is a per-repository error and doesn't stop the other repositories.
  - With JavaScript (`/repos.js`, included by both pages) rows are added and removed in place; removing the last row clears it, "+ Add repository" is disabled at 10 rows. Pasting several lines (`<url>` or `<url> <ref>`) into a URL field fills one row per line.
  - Without JavaScript the buttons submit the form with `add=1` or `remove=<row index>`; the server shows the edited form (a new empty row gets the focus) and scans nothing. Enter in any field starts the search, not a remove button.
  - The pages link to each other with the current list and `edit=1`, which only prefills the other page's form (e.g. "Cluster these repositories →"), so clustering never starts from a link.
- **UI (main page):** on submit it shows the skills of all listed repositories as one list sorted by name (ties keep the repository order), with the same data as `list-skills` (name, description, path), rendered as a clean card list. With several repositories each skill also shows its repository (`<url> [ref]`) and the summary says `N skills found in M repositories`. The form uses `GET` (see **Repository list**), so result pages can be bookmarked and shared; the old single-repository links `/?repo=<url>&ref=<ref>` keep working. The page title is the repository URL, or `N repositories` for several.
- **Per-repository errors (main page):** repositories that fail (invalid URL, clone or scan failure) are listed in a "Some repositories could not be scanned" block and the skills of the others are shown (status 200). If no repository could be scanned, the failures are shown as an error and the status is that of the first failure (400 invalid URL, 502 clone failure, 500 scan failure); with a single repository this is its error message, as before. SKILL.md parse warnings are prefixed with the repository when there are several.
- **Filter by name:** the form has an optional "filter" input; the query gets `&filter=<text>`, so filtered pages can be bookmarked and shared too. Filtering happens on the server (works without JavaScript) and is web UI only — `list-skills` has no filter. Rules:
  - case-insensitive substring match on the skill `name` only (not the description or path); the filter is trimmed, and an empty filter shows all skills;
  - the filter applies to the merged list of all repositories;
  - with an active filter the page shows a count such as `3 of 12 skills match "code"`, or `No skills match "xyz"` when nothing matches (distinct from "No skills found" for repositories without skills), plus a "Clear filter" link to the same repository list without the filter;
  - the filter text is HTML-escaped when echoed back;
  - the filter does not affect recent searches: entries stay keyed by the repository list and record the total (unfiltered) number of skills.
- **Skill text:** each skill card can be expanded by clicking it to show the full raw text of its `SKILL.md` (frontmatter included) in a monospace block; cards are collapsed by default. Works without JavaScript. Files larger than 256 KiB are truncated with a visible "truncated" note. The text is HTML-escaped.
- **Recent searches:** the page shows a list of recent successful searches (repositories with refs if set, total number of skills found), most recent first, each a link to its result page (`/?repo=…&ref=…`). Rules:
  - only searches in which every repository was scanned without an error are recorded (including "no skills found");
  - repeating a search moves it to the top instead of adding a duplicate (same repositories + refs in the same order);
  - at most 10 entries are kept;
  - history lives in the server's memory: it is shared by all visitors and lost on restart.
- **Input:** only remote repository URLs (`https://`, `http://`, `ssh://`, `git://`, `user@host:path`). Unlike the CLI, local paths, `file://` and other transports are rejected, so visitors cannot scan the server's filesystem.
- **Errors:** clone failures and "no skills found" are shown as messages in the UI.
- **Skill clustering (`/cluster`):** a separate page (linked from the main page and back) that groups the skills of several repositories by meaning using Claude.
  - **Form:** the shared repository list (see **Repository list**), submitted as `GET /cluster?repo=<url>&ref=<ref>&repo=…`, so results can be bookmarked. Works without JavaScript.
  - **Input:** the rules of the repository list (blank rows skipped, duplicates dropped, at most 10, remote-only URLs). The former textarea query `GET /cluster?repos=<one "<url>" or "<url> <ref>" per line>` is still accepted for old links; there a line with more than two fields is a per-repository "invalid line" error.
  - **Scanning:** repositories are cloned in parallel (same shallow clone as the main page) and their skills are discovered. Per-repository errors (invalid URL or line, clone failure) are listed in the UI and don't stop the other repositories.
  - **Clustering:** every skill gets an id (`s1`, `s2`, … in repository order, then by name). Only the ids, names and descriptions (truncated to 500 characters) are sent to Claude — no URLs, paths or file contents. Atlas runs `<claude-bin> -p --output-format json` with the prompt on stdin (in the system temp directory), takes the `result` field of the JSON envelope (output that is not an envelope is used as is) and parses the model answer as a JSON array `[{"name", "description", "skills": [<ids>]}]`; the outermost `[...]` is extracted, so markdown fences or surrounding prose are tolerated.
  - **Validation of the answer:** unknown or non-string ids are ignored; a skill listed in several clusters stays in the first one; clusters left without skills are dropped; a cluster without a name is called "Unnamed cluster"; skills the model left out go into an extra "Other" cluster. So every skill appears in exactly one cluster.
  - **Output:** cluster cards (name, description, number of skills) with their skills (name, description, repository + ref, path), plus the list of scanned repositories with skill counts.
  - **Errors (shown in the UI):** `claude` binary not found, non-zero exit, timeout, an error envelope (`is_error`), invalid JSON in the answer. If no skills are found, Claude is not called and "No skills found" is shown.
  - **Dependency:** requires [Claude Code](https://claude.com/claude-code) (`claude` CLI) installed and authenticated on the server. The rest of the UI works without it.
- **Progress:** while a scan (main page) or clustering (`/cluster`) runs, the page shows the current stage and a percentage with a progress bar. This is progressive enhancement: without JavaScript the forms submit normally and the final page is shown as before.
  - With JavaScript (`/progress.js`, included by both pages) the form submit is intercepted (except the add/remove buttons, which only edit the form): the script generates a random job id, fetches the same bookmarkable `GET` URL with the header `X-Atlas-Progress: <id>` and polls `GET /progress?id=<id>` (every ~0.5 s) until the page arrives; then it replaces the document with the result and puts the URL into the address bar (`history.pushState`), so results stay bookmarkable and HTTP statuses are unchanged.
  - `GET /progress?id=<id>` returns `{"stage": "<text>", "percent": <0-100>, "done": <bool>}` (`Cache-Control: no-store`), or `404` for an unknown id. Ids are 16–64 characters `[A-Za-z0-9_-]`; an invalid or already running id, or more than 100 jobs at once, just disables progress for that request. A finished job stays visible for 30 seconds, then it is forgotten. Progress lives in the server's memory.
  - Both pages scan repositories in parallel with the same stages: `Validating…`, `Cloning <repo>…` (one repository) or `Cloning N repositories…`, `Cloned <repo> (k/N), discovering skills…`, `Scanned <repo> (k/N)…`; then `/cluster` goes on with `Clustering M skills with Claude…`; the last stage is `Done`. Repository URLs in stages have credentials (user info) removed.
  - Percent is computed from weighted steps per repository (a failed repository still counts its steps): main page — cloning 80%, discovery and reading the SKILL.md files 20%; `/cluster` — cloning and discovery weigh the same and together make the first 50%, the Claude call is the other 50%.
  - Cloning advances gradually with `git clone --progress`: receiving objects is 80% of a clone, resolving deltas 15%, updating files 5%. A request waiting for another request's clone of the same repository (see **Repository cache**) completes its cloning stage at once when that clone ends. Percent never goes down, stays at most 99% while work is running and becomes 100% when the request completes (also on errors).
- **Background dancer:** both pages show a decorative cartoon of a dancing Trump (inline SVG, CSS animation: swaying, fist pumps, an oversized tie) fixed in the bottom-right corner behind the content. It is `aria-hidden`, ignores the pointer, fades to 30% opacity on windows narrower than 1260px (where it overlaps the content) and stands still with `prefers-reduced-motion`. No external assets, no JavaScript.
- **Logging:** the server logs to stderr with `log/slog` (text format, level INFO); the `Serving atlas on …` line stays on stdout.
  - Every request: `request started` (method, path) and `request finished` (method, path, status, duration), with a request number `req` shared by all lines of that request. Only the path is logged, not the query. `/progress`, `/progress.js` and `/repos.js` requests are logged at DEBUG level (hidden by default) so polling doesn't flood the log.
  - Stages of work: main page — `scan: validating`, `scan: repositories parsed` (valid/invalid counts), per repository `scan: cloning`, `scan: clone done` / `scan: clone failed` (duration, error), `scan: discovering skills`, `scan: skills found` (count), then `scan: done` (total skills); `/cluster` — `cluster: validating`, `cluster: repositories parsed` (valid/invalid counts), per repository `cluster: cloning`, `cluster: clone done` / `cluster: clone failed` (duration), `cluster: discovering skills`, `cluster: skills found`, then `cluster: calling Claude` (number of skills), `cluster: Claude done` (duration, number of clusters) or `cluster: Claude failed` / `cluster: invalid Claude answer` (duration, error).
  - Repository cache (one logger for everything, no separate setup): `repo cache hit`, `repo cache miss, cloning`, `repo cache expired, cloning again` (repo, ref; inside the request, with its `req`, between `scan: cloning`/`cluster: cloning` and the matching `clone done`) and `repo cache evict` (checkout directory).
  - No secrets or contents are logged: repository URLs (also inside error messages) have user info removed, and neither `SKILL.md` texts nor the Claude prompt/answer are logged.
- **Repository cache:** the web UI (main page and `/cluster`) reuses cloned repositories instead of cloning them on every request. `list-skills` is not affected: it always clones into a temp directory and removes it.
  - **Key:** normalized repository URL + ref. Normalization lowercases the scheme and host and drops trailing `/` and `.git`, so `https://GitHub.com/org/repo.git` and `https://github.com/org/repo` share an entry; different refs (including an empty ref, the default branch) are separate entries. The directory of an entry is named after a SHA-256 hash of the key, so user input never becomes a path.
  - **Freshness:** a checkout is reused for `--cache-ttl` (default `10m`) after it was cloned; the next request after that clones it again (fresh shallow clone, the old checkout is removed). `--cache-ttl 0` disables the cache: every request clones into a temp directory, as before.
  - **Size:** at most `--cache-size` repositories (default 50) are kept; adding one more evicts the least recently used.
  - **Concurrency:** concurrent requests for the same repository + ref (including parallel clones on `/cluster`) clone it once: the others wait and reuse the result, or get the same clone error. A checkout is never re-cloned or removed while a request is reading it — refresh and eviction wait until those requests finish.
  - **Errors:** failed clones are not cached; the next request tries again.
  - **Location:** each server process creates its own directory `atlas-cache-*` under `--cache-dir` (defaults to the system temp directory) and removes it on shutdown (SIGINT/SIGTERM). The cache is not reused across restarts; a crashed server leaves its directory behind, to be cleaned up with the rest of the temp directory.
  - **Logging:** through the server's logger (see **Logging** above).
  - **Progress:** a cache hit returns the checkout immediately, so the cloning stage completes at once and the request goes on through the usual stages to 100%.
- **Flags:**
  - `--addr <host:port>` — listen address (defaults to `localhost:8080`);
  - `--claude-bin <path>` — Claude Code CLI used by `/cluster` (defaults to `claude`, looked up in `PATH`);
  - `--claude-timeout <duration>` — timeout of one Claude call (defaults to `3m`);
  - `--cache-dir <path>` — parent directory of the repository cache (defaults to the system temp directory);
  - `--cache-ttl <duration>` — how long a cloned repository is reused (defaults to `10m`, `0` disables the cache);
  - `--cache-size <n>` — maximum number of cached repositories (defaults to 50).

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
  demo/                    # web UI demo recorder (Node, Playwright) and scenarios; not part of the build
  ```
- Unit tests for skill discovery and parsing, for the web UI handler (repository list: several repositories, add/remove without JavaScript, links between pages; skill text, recent searches, name filter, clustering with a fake Claude runner — unit tests never call the real Claude, progress tracking and the `/progress` endpoint, logging), and for the repository cache (hit/miss, TTL, eviction, concurrent requests cloning once, cache hits through the progress stages — with a fake clone function and a local git repository).
- Integration tests (build tag `integration`, `make test-integration`) check the whole pipeline: they build the `atlas` binary and run it against small real public GitHub repositories (clone → discovery → parsing → output). The `/cluster` integration tests pass a fake `claude` script via `--claude-bin` that prints a canned JSON envelope, so CI needs neither Claude nor an API key. Both unit and integration tests run in CI (GitHub Actions).

## Installation
- `go install github.com/<org>/atlas/cmd/atlas@latest`
- `scripts/install.sh` — installs from a local clone (`go install ./cmd/atlas`), checks that Go is installed and that `$(go env GOPATH)/bin` is in `PATH`.
- `Makefile`: `build`, `install`, `test`, `test-integration`.
- `scripts/demo-record.sh <scenario>` — records a web UI demo GIF (Node + Playwright, see `.claude/skills/demo`).
