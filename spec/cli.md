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
- **Skill text:** each skill card can be expanded by clicking it to show the full raw text of its `SKILL.md` (frontmatter included) in a monospace block; cards are collapsed by default. Works without JavaScript. Files larger than 256 KiB are truncated with a visible "truncated" note. The text is HTML-escaped.
- **Recent searches:** the page shows a list of recent successful searches (repo, ref if set, number of skills found), most recent first, each a link to its result page (`/?repo=…&ref=…`). Rules:
  - only searches that completed without an error are recorded (including "no skills found");
  - repeating a search moves it to the top instead of adding a duplicate (same repo + ref);
  - at most 10 entries are kept;
  - history lives in the server's memory: it is shared by all visitors and lost on restart.
- **Input:** only remote repository URLs (`https://`, `http://`, `ssh://`, `git://`, `user@host:path`). Unlike the CLI, local paths, `file://` and other transports are rejected, so visitors cannot scan the server's filesystem.
- **Errors:** clone failures and "no skills found" are shown as messages in the UI.
- **Flags:**
  - `--addr <host:port>` — listen address (defaults to `localhost:8080`).

## Technical requirements
- Go 1.22+, CLI built with [cobra](https://github.com/spf13/cobra).
- Cloning via system `git` (or `go-git`).
- Frontmatter parsing with `gopkg.in/yaml.v3`.

- Layout:
  ```
  cmd/atlas/main.go        # entry point
  internal/cli/            # cobra commands (root, list-skills, serve + embedded HTML page)
  internal/skills/         # SKILL.md discovery and parsing
  test/integration/        # integration tests
  ```
- Unit tests for skill discovery and parsing, and for the web UI handler (skill text, recent searches).
- Integration tests (build tag `integration`, `make test-integration`) check the whole pipeline: they build the `atlas` binary and run it against small real public GitHub repositories (clone → discovery → parsing → output). Both unit and integration tests run in CI (GitHub Actions).

## Installation
- `go install github.com/<org>/atlas/cmd/atlas@latest`
- `scripts/install.sh` — installs from a local clone (`go install ./cmd/atlas`), checks that Go is installed and that `$(go env GOPATH)/bin` is in `PATH`.
- `Makefile`: `build`, `install`, `test`, `test-integration`.
