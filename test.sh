#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "============================================"
echo "  NFTouch Server - 回归测试套件"
echo "============================================"
echo ""

cd "$SCRIPT_DIR"

echo "▶ 步骤 1/3: 运行单元测试..."
if go test -v -count=1 -timeout 30s ./... 2>&1; then
    echo "  ✓ 所有测试通过"
else
    echo "  ✗ 测试失败，请检查上方错误信息"
    exit 1
fi

echo ""
echo "▶ 步骤 2/3: 运行 go vet 静态分析..."
if go vet ./... 2>&1; then
    echo "  ✓ 静态分析通过"
else
    echo "  ✗ 静态分析发现问题"
    exit 1
fi

echo ""
echo "▶ 步骤 3/3: 验证编译..."
if go build -o /dev/null . 2>&1; then
    echo "  ✓ 编译成功"
else
    echo "  ✗ 编译失败"
    exit 1
fi

echo ""
echo "============================================"
echo "  ✓ 全部检查通过"
echo "============================================"
