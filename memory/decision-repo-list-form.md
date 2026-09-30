---
type: decision
date: 2026-09-30
description: Both pages share one repository list form: repeated repo/ref pairs by position, add/remove work without JS
---
Rows are submitted as `repo=…&ref=…` pairs matched by index (browsers keep DOM order), so the old
`/?repo=&ref=` links still work; `/cluster?repos=` (textarea) is accepted only for old links. Without JS
"+ Add"/"×" are submit buttons (`add=1`, `remove=<i>`) and the server re-renders the form without scanning;
`edit=1` does the same for the cross-page links, so clustering never starts from a link.

**Gotcha:** Enter in a field "clicks" the first submit button of the form — that would be a "×". An
off-screen `.implicit-submit` button comes first in the form (not `display:none`, which browsers skip).
Demo scenarios must click `button.primary`, not `button[type=submit]`.
**How to apply:** new pages with a repository list use the `repo-rows`/`repo-add`/`repo-css` templates from
`internal/cli/repos.html` (`pageTemplate`) and `scanRepos` with their own `scanPlan`.
