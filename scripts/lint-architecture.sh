#!/bin/bash
# 架构 linter — 在开发阶段拦截架构违规
# 用法: bash scripts/lint-architecture.sh
# 返回 0 表示通过；返回 1 表示有 Blocker/Major 级违规，**或**脚本无法真正扫描
# （缺少必需目录、或某一项检查未匹配到任何待检查文件）。
#
# 两条硬性约定：
#   1. 仓库根目录由 ${BASH_SOURCE[0]} 解析，不依赖 $PWD，脚本随后 cd 到仓库根。
#      因此 `cd 任意目录 && bash /绝对路径/scripts/lint-architecture.sh` 都能正确工作。
#   2. 任何一项检查只要没有真正扫到文件，就绝不打印 PASSED。
#      grep 找不到目录时会以非 0 退出；若把 stderr 丢进 /dev/null 又不校验返回码，
#      「没有违规」与「根本没扫」就变得不可区分——这正是本脚本必须避免的陷阱。

set -e

# ── 定位仓库根目录（不依赖 $PWD）──────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

ERRORS=0

echo "=== Architecture Linter ==="
echo "repo root: $REPO_ROOT"

# ── 前置断言：确认每一项检查都有真实的扫描目标，否则不得宣称通过 ──────────────
echo "→ Verifying scan roots..."
BAD_ROOTS=""
for d in domain application common infrastructure infrastructure/controller; do
    if [ ! -d "$d" ]; then
        BAD_ROOTS="$BAD_ROOTS $d/(目录缺失)"
    elif [ -z "$(find "$d" -name '*.go' -print -quit)" ]; then
        BAD_ROOTS="$BAD_ROOTS $d/(无 .go 文件)"
    fi
done
if [ -n "$BAD_ROOTS" ]; then
    echo "BLOCKER: 无法扫描，以下扫描根不可用:$BAD_ROOTS"
    echo "         请确认脚本位于仓库根的 scripts/ 目录下，且各分层目录存在。"
    exit 1
fi
echo "  ✅ scan roots present"

# ── 字符串字面量提取器 ─────────────────────────────────────────────────────────
# extract_imports <dir> <skip_tests:0|1>
#   import 路径必然写在字符串字面量里，所以先摘出引号内容，输出 file:line:path（已去引号）。
#   关键点：**不要**再用 `grep -v "// "` 过滤整行——那会把
#   `import _ ".../x" // 中文说明` 这种带行尾注释的真实违规整行丢掉。
#   本仓库中文行尾注释是常规写法，所以改为「先摘引号内容、再对内容匹配」，
#   行尾注释自然不再干扰匹配。
extract_imports() {
    local dir="$1" skip_tests="$2" raw
    raw="$(grep -rnoE '"[^"]+"' "$dir" 2>/dev/null || true)"
    [ -z "$raw" ] && return 0
    if [ "$skip_tests" = "1" ]; then
        raw="$(printf '%s\n' "$raw" | grep -v '_test\.go:' || true)"
        [ -z "$raw" ] && return 0
    fi
    printf '%s\n' "$raw" | sed -E 's/^([^:]+:[0-9]+):"(.*)"$/\1:\2/'
}

# 1. domain/ 禁止 import gin/gorm/redis/http/infrastructure/application
# 注意必须一并检查内部包全路径：只查 gin/gorm 这些短名会放过
# `domain/ → infrastructure/...`、`domain/ → application/...` 这类跨层违规，
# 而 README 明确宣称本检查覆盖它们（check #3 对 common/ 用的就是全路径）。
echo "→ Checking domain/ import redlines..."
HITS="$(extract_imports domain 1 | grep -E 'gin-gonic/gin|gorm\.io/gorm|go-redis/v9|net/http|github\.com/PycMono/FastRAG/(infrastructure|application)/' || true)"
if [ -n "$HITS" ]; then
    printf '%s\n' "$HITS"
    echo "BLOCKER: domain/ imports forbidden package (gin/gorm/redis/http/infrastructure/application)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ domain/ imports clean"
fi

# 2. application/ 禁止 import gin / infrastructure/driver / infrastructure/controller
# 与规则 1 同理：必须覆盖整棵 infrastructure/driver/ 子树，
# 只列 driver/redis、driver/mysql 会放过 driver/gingext，以及今后新增的任何 driver。
echo "→ Checking application/ import redlines..."
HITS="$(extract_imports application 1 | grep -E 'gin-gonic/gin|infrastructure/(driver|controller)/' || true)"
if [ -n "$HITS" ]; then
    printf '%s\n' "$HITS"
    echo "BLOCKER: application/ imports forbidden package (gin/infrastructure/driver/infrastructure/controller)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ application/ imports clean"
fi

# 3. common/ 禁止 import domain/application/infrastructure
echo "→ Checking common/ import redlines..."
HITS="$(extract_imports common 1 | grep -E 'github\.com/PycMono/FastRAG/(domain|application|infrastructure)/' || true)"
if [ -n "$HITS" ]; then
    printf '%s\n' "$HITS"
    echo "BLOCKER: common/ imports business layer (domain/application/infrastructure)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ common/ imports clean"
fi

# 4. Controller 禁止直接返回 Entity（启发式：Controller 不应 import domain/entity）
echo "→ Checking Controller entity leakage..."
CTRL_FILES=0
CTRL_BAD=0
for f in infrastructure/controller/http/*/*.go; do
    if [ -f "$f" ]; then
        CTRL_FILES=$((CTRL_FILES + 1))
        if grep -q '"github\.com/PycMono/FastRAG/domain/entity' "$f"; then
            echo "MAJOR: Controller $f imports domain/entity — may return Entity instead of VO"
            ERRORS=$((ERRORS + 1))
            CTRL_BAD=$((CTRL_BAD + 1))
        fi
    fi
done
if [ "$CTRL_FILES" -eq 0 ]; then
    echo "BLOCKER: infrastructure/controller/http/*/*.go 未匹配到任何文件，无法检查"
    ERRORS=$((ERRORS + 1))
elif [ "$CTRL_BAD" -eq 0 ]; then
    echo "  ✅ Controller entity check done"
fi

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
FMT_FILES=0
FMT_BAD=0
for f in infrastructure/controller/http/*/*.go; do
    if [ -f "$f" ]; then
        FMT_FILES=$((FMT_FILES + 1))
        if [ "$(dirname "$f")" = "infrastructure/controller/http/health" ]; then
            echo "  ⏭️  Skipping $f (health probe needs fixed format)"
            continue
        fi
        # 先剥掉行尾注释再匹配：本仓库中文行尾注释是常规写法，
        # `c.JSON(200, nil) // 临时调试` 必须仍然判为违规。
        # 这里匹配的是一个调用而不是一个路径，所以不能沿用规则 1–3 的「提取引号字面量」做法，
        # 改为从 `//` 截断到行尾。对该规则是安全的：只有当真实调用被写在 `//` 之后才会误伤，
        # 而那种写法不存在；`c.JSON(200, "http://…")` 截断后仍然含有 `c.JSON(`。
        if sed 's|//.*$||' "$f" 2>/dev/null | grep -n 'c\.JSON('; then
            echo "BLOCKER: $f uses c.JSON() instead of gingext.Send()"
            ERRORS=$((ERRORS + 1))
            FMT_BAD=$((FMT_BAD + 1))
        fi
    fi
done
if [ "$FMT_FILES" -eq 0 ]; then
    echo "BLOCKER: infrastructure/controller/http/*/*.go 未匹配到任何文件，无法检查"
    ERRORS=$((ERRORS + 1))
elif [ "$FMT_BAD" -eq 0 ]; then
    echo "  ✅ Response format check done"
fi

# 总结
echo ""
if [ $ERRORS -gt 0 ]; then
    echo "=== FAILED: $ERRORS architecture violation(s) found ==="
    exit 1
else
    echo "=== PASSED: All architecture checks passed ==="
    exit 0
fi
