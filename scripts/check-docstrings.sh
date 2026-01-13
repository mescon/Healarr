#!/bin/bash
# check-docstrings.sh - Verify Go docstring coverage meets threshold
#
# Usage: ./scripts/check-docstrings.sh [threshold]
# Default threshold: 80%
#
# Exit codes:
#   0 - Coverage meets threshold
#   1 - Coverage below threshold

set -e

THRESHOLD=${1:-80}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

total=0
documented=0
missing_files=""

# Find all non-test Go files
while IFS= read -r -d '' file; do
    # Skip test files and generated files
    [[ "$file" == *"_test.go" ]] && continue
    [[ "$file" == *"/testutil/"* ]] && continue

    # Count exported identifiers (functions, types, methods, vars, consts)
    # Exported = starts with uppercase letter
    file_exports=0
    file_docs=0

    # Use awk for more accurate parsing
    while IFS= read -r line; do
        # Check if this is an exported identifier
        if [[ "$line" =~ ^func\ [A-Z] ]] || \
           [[ "$line" =~ ^type\ [A-Z] ]] || \
           [[ "$line" =~ ^var\ [A-Z] ]] || \
           [[ "$line" =~ ^const\ [A-Z] ]] || \
           [[ "$line" =~ ^[[:space:]]+[A-Z][a-zA-Z0-9_]*[[:space:]]+func ]]; then
            ((file_exports++)) || true
        fi
    done < "$file"

    # Count doc comments (// Comment before exported identifier)
    # A doc comment must immediately precede the declaration
    prev_line=""
    while IFS= read -r line; do
        if [[ "$line" =~ ^func\ [A-Z] ]] || \
           [[ "$line" =~ ^type\ [A-Z] ]] || \
           [[ "$line" =~ ^var\ [A-Z] ]] || \
           [[ "$line" =~ ^const\ [A-Z] ]]; then
            if [[ "$prev_line" =~ ^// ]]; then
                ((file_docs++)) || true
            fi
        fi
        prev_line="$line"
    done < "$file"

    total=$((total + file_exports))
    documented=$((documented + file_docs))

    # Track files with missing docs
    if [ "$file_exports" -gt 0 ] && [ "$file_docs" -lt "$file_exports" ]; then
        missing=$((file_exports - file_docs))
        missing_files="$missing_files\n  ${file#./}: $missing missing"
    fi

done < <(find internal cmd -name "*.go" -print0 2>/dev/null)

# Calculate percentage
if [ "$total" -eq 0 ]; then
    echo "No exported identifiers found"
    exit 0
fi

pct=$((documented * 100 / total))

echo "=================================="
echo "Go Docstring Coverage Report"
echo "=================================="
echo "Documented: $documented / $total ($pct%)"
echo "Threshold:  $THRESHOLD%"
echo "=================================="

if [ "$pct" -lt "$THRESHOLD" ]; then
    needed=$((total * THRESHOLD / 100 - documented + 1))
    echo ""
    echo "❌ FAILED: Coverage $pct% is below $THRESHOLD% threshold"
    echo "   Need $needed more doc comments to reach threshold"
    if [ -n "$missing_files" ]; then
        echo ""
        echo "Files with missing documentation:"
        echo -e "$missing_files"
    fi
    exit 1
else
    echo ""
    echo "✅ PASSED: Coverage meets threshold"
    exit 0
fi
