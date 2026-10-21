#!/usr/bin/env bash
# Print one changelog section. The argument is the release tag.
set -euo pipefail

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
	echo "usage: changelog-section.sh <tag>" >&2
	exit 2
fi

tag="$1"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="${root}/CHANGELOG.md"
prefix="## [${tag}]"

if [ ! -f "$file" ]; then
	echo "CHANGELOG.md is missing" >&2
	exit 1
fi

if ! awk -v prefix="$prefix" '
	function starts_section(line, heading,    n, rest) {
		n = length(heading)
		if (substr(line, 1, n) != heading) {
			return 0
		}
		rest = substr(line, n + 1)
		return rest == "" || substr(rest, 1, 1) == " " || substr(rest, 1, 1) == "\t"
	}
	BEGIN { found = 0; capture = 0 }
	starts_section($0, prefix) {
		found = 1
		capture = 1
		print
		next
	}
	capture && $0 ~ /^## \[/ {
		capture = 0
		next
	}
	capture { print }
	END { if (!found) exit 1 }
' "$file"; then
	echo "changelog has no section for ${tag}" >&2
	exit 1
fi
