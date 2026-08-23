#!/bin/sh
# coverage-gate.sh — enforce per-package statement coverage (AGENTS.md 门控).
# Usage: scripts/coverage-gate.sh [backend-dir] [min-percent]
# Reads $backend/coverage.out (produced by `go test -coverpkg=./internal/...`)
# and fails when any pure-logic domain package is below the threshold.
set -eu

BACKEND="${1:-backend}"
COV_MIN="${2:-70}"

COV_FILE="$BACKEND/coverage.out"
[ -f "$COV_FILE" ] || { echo "coverage.out missing — run make test first"; exit 1; }

PURE_PKGS="internal/pricing internal/platform internal/platform/uu internal/platform/eco \
internal/platform/steam internal/recon internal/analytics internal/auth internal/secrets \
internal/config"

# The merged profile repeats each instrumented block once per test package;
# collapse duplicates by keeping the max hit count per block before aggregating.
awk '{ key = $1;
	if (!(key in stmts) || $2 > stmts[key]) stmts[key] = $2;
	if (!(key in cnt) || $3 > cnt[key]) cnt[key] = $3 }
	END { for (k in cnt) {
		split(k, loc, ":");
		idx = index(loc[1], "/internal/");
		if (idx == 0) continue;
		p = substr(loc[1], idx + 1);
		sub(/\/[^\/]*$/, "", p);
		tot[p] += stmts[k]; if (cnt[k] > 0) cov[p] += stmts[k] }
		for (p in tot) printf "%s %d\n", p, 100 * cov[p] / tot[p] }' "$COV_FILE" \
	| sort > "$COV_FILE.by-pkg"

fail=0
for pkg in $PURE_PKGS; do
	pct=$(awk -v p="$pkg" '$1==p{print $2}' "$COV_FILE.by-pkg")
	if [ -z "$pct" ]; then
		echo "FAIL $pkg: no coverage data"
		fail=1
	elif [ "$pct" -lt "$COV_MIN" ]; then
		echo "FAIL $pkg ${pct}% < ${COV_MIN}%"
		fail=1
	else
		echo "ok   $pkg ${pct}%"
	fi
done
rm -f "$COV_FILE.by-pkg"
exit $fail
