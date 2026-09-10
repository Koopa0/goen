#!/bin/sh

# Compare x/tools/deadcode's report with a reasoned, bidirectional allowlist.
# Unknown dead functions and stale exceptions are both failures: otherwise the
# allowlist can silently outlive the debt it was meant to name.
set -eu

if [ "$#" -ne 2 ]; then
	echo 'usage: deadcode-check.sh REPORT ALLOWLIST' >&2
	exit 2
fi

report=$1
allowlist=$2
[ -r "$report" ] || { echo "deadcode: cannot read report: $report" >&2; exit 2; }
[ -r "$allowlist" ] || { echo "deadcode: cannot read allowlist: $allowlist" >&2; exit 2; }

work=$(mktemp -d "${TMPDIR:-/tmp}/goen-deadcode-check.XXXXXX")
cleanup_deadcode_check() {
	status=$?
	trap - EXIT HUP INT TERM
	rm -rf "$work"
	exit "$status"
}
trap cleanup_deadcode_check EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

awk '
function trim(s) {
	sub(/^[[:space:]]+/, "", s)
	sub(/[[:space:]]+$/, "", s)
	return s
}
{
	raw = $0
	line = trim($0)
	if (line == "" || line ~ /^#/) {
		next
	}
	marker = index(line, "# reason:")
	if (marker == 0) {
		printf "deadcode: invalid allowlist line %d: every entry requires # reason: followed by a reason\n", NR > "/dev/stderr"
		bad = 1
		next
	}
	symbol = trim(substr(line, 1, marker - 1))
	reason = trim(substr(line, marker + length("# reason:")))
	if (symbol !~ /^[A-Za-z0-9_.\/-]+\.[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$/) {
		printf "deadcode: invalid symbol on allowlist line %d: %s\n", NR, symbol > "/dev/stderr"
		bad = 1
		next
	}
	if (reason == "") {
		printf "deadcode: invalid allowlist line %d: # reason: must not be empty\n", NR > "/dev/stderr"
		bad = 1
		next
	}
	print symbol
}
END { if (bad) exit 1 }
' "$allowlist" > "$work/allow.ids"

LC_ALL=C sort "$work/allow.ids" > "$work/allow.sorted"
LC_ALL=C uniq -d "$work/allow.sorted" > "$work/allow.duplicates"
if [ -s "$work/allow.duplicates" ]; then
	echo 'deadcode: duplicate allowlist entries:' >&2
	sed 's/^/  /' "$work/allow.duplicates" >&2
	exit 1
fi

awk '
{
	raw = $0
	parts = split(raw, half, ": unreachable func: ")
	if (parts != 2) {
		printf "deadcode: unrecognised tool output on line %d: %s\n", NR, raw > "/dev/stderr"
		bad = 1
		next
	}
	location_parts = split(half[1], location, ":")
	path = location[1]
	line = location[2]
	column = location[3]
	symbol = half[2]
	if (location_parts != 3 || path !~ /^[A-Za-z0-9_.\/-]+\.go$/ || path ~ /^\// || path ~ /(^|\/)\.\.($|\/)/ || line !~ /^[0-9]+$/ || column !~ /^[0-9]+$/ || symbol !~ /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$/) {
		printf "deadcode: unrecognised tool output on line %d: %s\n", NR, raw > "/dev/stderr"
		bad = 1
		next
	}
	path_parts = split(path, path_component, "/")
	package_path = path_component[1]
	for (i = 2; i < path_parts; i++) {
		package_path = package_path "/" path_component[i]
	}
	identity = package_path "." symbol
	print identity "\t" raw
}
END { if (bad) exit 1 }
' "$report" > "$work/report.map"

cut -f1 "$work/report.map" > "$work/report.ids"
LC_ALL=C sort "$work/report.ids" > "$work/report.sorted"
LC_ALL=C uniq -d "$work/report.sorted" > "$work/report.duplicates"
if [ -s "$work/report.duplicates" ]; then
	echo 'deadcode: the tool reported duplicate symbol identities:' >&2
	sed 's/^/  /' "$work/report.duplicates" >&2
	exit 1
fi

LC_ALL=C comm -23 "$work/report.sorted" "$work/allow.sorted" > "$work/unexpected"
LC_ALL=C comm -13 "$work/report.sorted" "$work/allow.sorted" > "$work/stale"

failed=0
if [ -s "$work/unexpected" ]; then
	failed=1
	echo 'deadcode: unexpected unreachable functions:' >&2
	awk -F '\t' 'NR == FNR { unexpected[$1] = 1; next } $1 in unexpected { print "  " $2 }' \
		"$work/unexpected" "$work/report.map" >&2
	echo 'Wire each one or delete it. Nothing here is public API, so an unused export is a feature nobody finished.' >&2
fi
if [ -s "$work/stale" ]; then
	failed=1
	echo 'deadcode: stale allowlist entries:' >&2
	while IFS= read -r symbol; do
		echo "  $symbol: this entry names a function that is reachable now; delete the line" >&2
	done < "$work/stale"
fi

[ "$failed" -eq 0 ] || exit 1
count=$(awk 'END { print NR + 0 }' "$work/report.sorted")
echo "deadcode: PASS — $count unreachable functions, all explicitly reasoned"
