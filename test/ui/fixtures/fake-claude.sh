#!/bin/sh
# Stands in for `claude -p` on /cluster: ignores the prompt, prints a canned answer.
# Skill ids: alpha — code-review s1, commit s2, deploy s3, docs-writer s4; beta — doc-search s5, release-notes s6.
cat >/dev/null
cat <<'JSON'
{"type":"result","subtype":"success","is_error":false,"result":"[{\"name\":\"Git workflow\",\"description\":\"Commits and releases\",\"skills\":[\"s2\",\"s6\"]},{\"name\":\"Documentation\",\"description\":\"Writing and finding docs\",\"skills\":[\"s4\",\"s5\"]},{\"name\":\"Code quality\",\"description\":\"Reviewing changes\",\"skills\":[\"s1\"]}]"}
JSON
