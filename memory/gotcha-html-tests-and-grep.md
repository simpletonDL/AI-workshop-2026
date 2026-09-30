---
type: gotcha
date: 2026-09-30
description: Substring checks on serve HTML hit the inline CSS — assert on markup, count skills via the CLI
---
The page embeds CSS, so words like `truncated` matched the stylesheet and gave false positives, both in
tests and when grepping the served page. Integration tests look for `<h2>name</h2>`.
**How to apply:** assert on specific markup; to count a repo's skills use `atlas list-skills --json`, not
grep on HTML. The rendered UI (progress bar included) is covered by the screenshot tests in `test/ui` (see `decision-ui-screenshot-tests.md`).
