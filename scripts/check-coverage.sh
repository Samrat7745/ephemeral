#!/usr/bin/env bash
#
# Copyright (c) 2026 - for information on the respective copyright owner
# see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
#
# SPDX-License-Identifier: Apache-2.0
#
# Fails if the total statement coverage of a Go coverage profile is below the floor.
#
# Usage: scripts/check-coverage.sh [PROFILE] [FLOOR]
#   PROFILE  coverage profile written by `go test -coverprofile` (default: coverage.txt)
#   FLOOR    minimum total coverage in percent (default: $COVERAGE_FLOOR or the value below)
#
# Like the Codecov upload, fakes and test helpers are excluded from the measurement.
# The floor is the measured total minus 2 points, raise it when coverage improves.
set -euo pipefail

DEFAULT_FLOOR=82.7
profile="${1:-coverage.txt}"
floor="${2:-${COVERAGE_FLOOR:-$DEFAULT_FLOOR}}"

if [[ ! -s "$profile" ]]; then
  echo "coverage profile '$profile' not found or empty" >&2
  exit 2
fi
if ! [[ "$floor" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "invalid coverage floor '$floor'" >&2
  exit 2
fi

filtered="$(mktemp)"
trap 'rm -f "$filtered"' EXIT
grep -Ev "fake|helper" "$profile" > "$filtered" || true

total="$(go tool cover -func="$filtered" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"
if [[ -z "$total" ]]; then
  echo "could not determine the total coverage of '$profile'" >&2
  exit 2
fi

echo "total coverage: ${total}% (floor: ${floor}%)"
if awk -v t="$total" -v f="$floor" 'BEGIN { exit !(t + 0 < f + 0) }'; then
  echo "coverage ${total}% is below the floor of ${floor}%" >&2
  exit 1
fi
