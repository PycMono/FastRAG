#!/bin/bash
# 架构 linter — 在开发阶段拦截架构违规
# 用法: bash scripts/lint-architecture.sh
# 返回 0 表示通过，返回 1 表示有 Blocker 级违规

set -e

ERRORS=0

echo "=== Architecture Linter ==="

# 1. domain/ 禁止 import gin/gorm/redis/http/infrastructure/application
# 注意必须一并检查内部包全路径：只查 gin/gorm 这些短名会放过
# `domain/ → infrastructure/...`、`domain/ → application/...` 这类跨层违规，
# 而 README 明确宣称本检查覆盖它们（check #3 对 common/ 用的就是全路径）。
echo "→ Checking domain/ import redlines..."
if grep -rnE 'gin-gonic/gin|gorm\.io/gorm|go-redis/v9|net/http|"github.com/PycMono/FastRAG/(infrastructure|application)/' domain/ 2>/dev/null | grep -v "_test.go" | grep -v "// "; then
    echo "BLOCKER: domain/ imports forbidden package (gin/gorm/redis/http/infrastructure/application)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ domain/ imports clean"
fi

# 2. application/ 禁止 import gin/redis driver/mysql driver/controller
echo "→ Checking application/ import redlines..."
if grep -rn "gin-gonic/gin\|driver/redis\|driver/mysql\|infrastructure/controller" application/ 2>/dev/null | grep -v "_test.go" | grep -v "// "; then
    echo "BLOCKER: application/ imports forbidden package (gin/redis driver/mysql driver/controller)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ application/ imports clean"
fi

# 3. common/ 禁止 import domain/application/infrastructure
echo "→ Checking common/ import redlines..."
if grep -rnE '"github.com/PycMono/FastRAG/domain/|"github.com/PycMono/FastRAG/application/|"github.com/PycMono/FastRAG/infrastructure/' common/ 2>/dev/null | grep -v "_test.go"; then
    echo "BLOCKER: common/ imports business layer (domain/application/infrastructure)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ common/ imports clean"
fi

# 4. Controller 禁止直接返回 Entity（启发式：Controller 不应 import domain/entity）
echo "→ Checking Controller entity leakage..."
for f in infrastructure/controller/http/*/*.go; do
    if [ -f "$f" ]; then
        if grep -q '"github.com/PycMono/FastRAG/domain/entity' "$f"; then
            echo "MAJOR: Controller $f imports domain/entity — may return Entity instead of VO"
            ERRORS=$((ERRORS + 1))
        fi
    fi
done
echo "  ✅ Controller entity check done"

# 5. API 路由必须在 api Group 下
echo "→ Checking API route grouping..."
if grep -rnE '\.(GET|POST|PUT|DELETE)\("/?api' infrastructure/controller/ 2>/dev/null; then
    echo "BLOCKER: API route registered on top-level router instead of api Group"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ API route grouping check done"
fi

# 6. 检查是否使用 gingext.Send 而非 c.JSON（health 探针豁免）
echo "→ Checking unified response format..."
for f in infrastructure/controller/http/*/*.go; do
    if [ -f "$f" ]; then
        if [ "$(dirname "$f")" = "infrastructure/controller/http/health" ]; then
            echo "  ⏭️  Skipping $f (health probe needs fixed format)"
            continue
        fi
        if grep -n 'c\.JSON(' "$f" 2>/dev/null | grep -v '//'; then
            echo "BLOCKER: $f uses c.JSON() instead of gingext.Send()"
            ERRORS=$((ERRORS + 1))
        fi
    fi
done
echo "  ✅ Response format check done"

# 总结
echo ""
if [ $ERRORS -gt 0 ]; then
    echo "=== FAILED: $ERRORS architecture violation(s) found ==="
    exit 1
else
    echo "=== PASSED: All architecture checks passed ==="
    exit 0
fi
