---
type: decision
date: 2026-09-30
description: atlas serve rejects local paths and file:// URLs — only remote repos
---
`list-skills` accepts local paths, but the web service accepts only `https://`, `http://`, `ssh://`,
`git://` and `git@host:path`.

**Why:** otherwise any visitor of the page could read files on the machine running the server.
**How to apply:** keep this check when touching URL handling in `serve`; logs hide logins inside URLs too.
