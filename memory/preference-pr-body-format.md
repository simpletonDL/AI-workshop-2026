---
type: preference
date: 2026-09-30
description: PR body = fixed sections from .github/pull_request_template.md, in the order the user chose
---
Order chosen by the user: short summary → demo → implementation details → tests (present or not) → other
important notes. Empty sections stay with "none" / "—" so it's visible nothing was forgotten
("_No UI changes._" in Demo). "Decisions made without asking" matters most for headless agent PRs.

**Why:** PRs are reviewed quickly; a stable structure shows at once what to check and what is missing.
**How to apply:** fill the template for every PR (`gh pr create --body-file`); don't add or reorder sections
without asking.
