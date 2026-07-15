#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "============================================"
echo "  NFTouch Server - Regression Test Suite"
echo "============================================"
echo ""

cd "$SCRIPT_DIR"

echo "▶ Step 1/3: Running unit tests..."
if go test -v -count=1 -timeout 30s ./... 2>&1; then
    echo "  ✓ All tests passed"
else
    echo "  ✗ Tests failed, check errors above"
    exit 1
fi

echo ""
echo "▶ Step 2/3: Running go vet static analysis..."
if go vet ./... 2>&1; then
    echo "  ✓ Static analysis passed"
else
    echo "  ✗ Static analysis found issues"
    exit 1
fi

echo ""
echo "▶ Step 3/3: Verifying build..."
if go build -o /dev/null . 2>&1; then
    echo "  ✓ Build successful"
else
    echo "  ✗ Build failed"
    exit 1
fi

echo ""
echo "============================================"
echo "  ✓ All checks passed"
echo "============================================"
