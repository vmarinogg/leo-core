#!/usr/bin/env bash
# Verify that the four MOM skills are byte-identical between the two
# install sources used by `mom init` / `mom upgrade`:
#
#   1. skills.sh source (momhq/mom):
#        skills/mom-{status,recall,project,wrap-up}/SKILL.md
#      Used by Claude, Codex, and Pi (skill content only).
#
#   2. pi-mom npm package source (vmarinogg/pi-packages):
#        pi-mom/skills/mom-{status,recall,project,wrap-up}/SKILL.md
#      Used by Pi for its deeper extension-based install.
#
# Pi is a hybrid: skills.sh writes the SKILL.md files AND `pi install
# npm:pi-mom` installs the full extension (which carries its own copy
# of the same skill files plus extra code). If the two sources drift,
# Pi users see different behavior depending on which install path ran
# last. This script enforces that they stay in lockstep.
#
# Usage:
#   scripts/verify-skills-sync.sh                # uses ../pi-packages
#   scripts/verify-skills-sync.sh /path/to/pkg   # custom path
#
# Exit 0 on match. Non-zero on drift or missing files.

set -euo pipefail

mom_repo="$(cd "$(dirname "$0")/.." && pwd)"
pi_packages_root="${1:-$(cd "$mom_repo/.." && pwd)/pi-packages}"

if [[ ! -d "$pi_packages_root/pi-mom/skills" ]]; then
  echo "error: pi-packages not found at $pi_packages_root" >&2
  echo "       pass the path as first arg or clone vmarinogg/pi-packages next to mom/" >&2
  exit 2
fi

skills=(mom-status mom-recall mom-project mom-wrap-up)
fail=0
for s in "${skills[@]}"; do
  a="$mom_repo/skills/$s/SKILL.md"
  b="$pi_packages_root/pi-mom/skills/$s/SKILL.md"
  if [[ ! -f "$a" ]]; then
    echo "missing in momhq/mom: $a" >&2
    fail=1
    continue
  fi
  if [[ ! -f "$b" ]]; then
    echo "missing in pi-mom:    $b" >&2
    fail=1
    continue
  fi
  if ! diff -q "$a" "$b" >/dev/null; then
    echo "DRIFT: $s/SKILL.md differs between momhq/mom and pi-mom" >&2
    diff -u "$a" "$b" || true
    fail=1
  fi
done

if [[ $fail -ne 0 ]]; then
  echo
  echo "Skills are OUT OF SYNC. Re-copy from the canonical source (momhq/mom):" >&2
  echo "  for s in ${skills[*]}; do" >&2
  echo "    cp \"$mom_repo/skills/\$s/SKILL.md\" \"$pi_packages_root/pi-mom/skills/\$s/SKILL.md\"" >&2
  echo "  done" >&2
  echo "Then bump pi-mom version, tag, and push." >&2
  exit 1
fi

echo "OK: skills synced ($pi_packages_root/pi-mom <-> $mom_repo)"
