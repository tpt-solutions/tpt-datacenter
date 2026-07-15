#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2024 TPT Solutions
# SPDX-License-Identifier: MIT OR Apache-2.0
#
# Verify that each staged source file carries an SPDX license identifier.
# Used by pre-commit (see .pre-commit-config.yaml).
set -euo pipefail

missing=0
while IFS= read -r -d '' f; do
  if ! grep -q "SPDX-License-Identifier" "$f"; then
    echo "Missing SPDX-License-Identifier: $f"
    missing=1
  fi
done < <(find . -type f \( -name '*.rs' -o -name '*.go' -o -name '*.py' \) \
  -not -path './target/*' -not -path './.git/*' -print0)

if [ "$missing" -ne 0 ]; then
  echo "Some files are missing SPDX headers. Add:"
  echo "// SPDX-FileCopyrightText: 2024 TPT Solutions"
  echo "// SPDX-License-Identifier: MIT OR Apache-2.0"
  exit 1
fi
