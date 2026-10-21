#!/usr/bin/env bash
# Scan the engine module and the dashboard module with govulncheck.
# Documented exceptions belong in SECURITY.md. Keep ALLOWED_VULNS in sync.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Space-separated GO-* IDs. Empty when no exceptions are active.
ALLOWED_VULNS=""

scan() {
	local dir="$1"
	local name="$2"
	local out status found id allowed unexpected

	out="$(mktemp)"
	set +e
	(cd "$dir" && govulncheck -format=text ./...) >"$out" 2>&1
	status=$?
	set -e

	cat "$out"

	if [[ $status -eq 0 ]]; then
		echo "govulncheck: no vulnerabilities affecting ${name} call paths"
		rm -f "$out"
		return 0
	fi

	if [[ $status -ne 3 ]]; then
		echo "govulncheck: unexpected exit status ${status} for ${name}" >&2
		rm -f "$out"
		exit "$status"
	fi

	found="$(grep -oE 'GO-[0-9]{4}-[0-9]+' "$out" | sort -u || true)"
	rm -f "$out"
	if [[ -z "$found" ]]; then
		echo "govulncheck: failed for ${name} but no GO-* IDs parsed; see output above" >&2
		exit 3
	fi

	unexpected=""
	while IFS= read -r id; do
		[[ -z "$id" ]] && continue
		allowed=false
		for a in $ALLOWED_VULNS; do
			if [[ "$id" == "$a" ]]; then
				allowed=true
				break
			fi
		done
		if [[ "$allowed" == false ]]; then
			unexpected="${unexpected} ${id}"
		fi
	done <<<"$found"

	if [[ -n "${unexpected// /}" ]]; then
		echo "govulncheck: unexpected vulnerabilities in ${name} (not listed in SECURITY.md):${unexpected}" >&2
		exit 3
	fi

	echo "govulncheck: only documented exceptions remain for ${name}; see SECURITY.md"
}

scan "." "engine"
scan "dashboard" "dashboard"
exit 0
