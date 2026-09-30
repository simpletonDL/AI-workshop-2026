---
type: gotcha
date: 2026-09-30
description: Substring checks on serve HTML hit the inline CSS — assert on markup, count skills via the CLI
---
The page embeds CSS, so words like `truncated` matched the stylesheet and gave false positives, both in
tests and when grepping the served page. Integration tests look for `<h2>name</h2>`.
**How to apply:** assert on specific markup; to count a repo's skills use `atlas list-skills --json`, not
grep on HTML. UI changes are only tested through HTML — nobody has checked progress bars in a real browser.
