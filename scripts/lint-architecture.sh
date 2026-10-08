#!/bin/bash
# 架构 linter — 在开发阶段拦截架构违规
# 用法: bash scripts/lint-architecture.sh
# 返回 0 表示通过；返回 1 表示有 Blocker/Major 级违规，**或**脚本无法真正扫描
# （缺少必需目录、扫描根下没有 .go 文件、或扫描期间出现读取错误）。
#
# 两条硬性约定：
#   1. 仓库根目录由 ${BASH_SOURCE[0]} 解析，不依赖 $PWD，脚本随后 cd 到仓库根。
#      因此 `cd 任意目录 && bash /绝对路径/scripts/lint-architecture.sh` 都能正确工作。
#   2. 任何一项检查只要没有真正扫到文件，就绝不打印 PASSED。
#      grep 找不到目录时会以非 0 退出；若把 stderr 丢进 /dev/null 又不校验返回码，
#      「没有违规」与「根本没扫」就变得不可区分。因此前置断言会逐个校验扫描根，
#      并检查扫描期间 stderr 是否出现任何读取错误（含子目录不可读），
#      只有全部通过才会继续往下跑规则。

set -e

# ── 定位仓库根目录（不依赖 $PWD）──────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

ERRORS=0

echo "=== Architecture Linter ==="
echo "repo root: $REPO_ROOT"

# ── 前置断言：确认每一项检查都有真实的、可完整读取的扫描目标 ──────────────────
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

# 子目录不可读时 find 会往 stderr 报错；此时 grep 会静默扫不到里面的违规，
# 于是「没扫到」又会被当成「没问题」。所以扫描期 stderr 非空一律视为失败。
# 注意 2>&1 必须先于 >/dev/null，这样捕获的是 stderr 而不是 stdout。
SCAN_ROOTS="domain application common infrastructure"
SCAN_ERR="$(find $SCAN_ROOTS -name '*.go' -print 2>&1 >/dev/null || true)"
if [ -n "$SCAN_ERR" ]; then
    echo "BLOCKER: 扫描期间出现读取错误，结果不可信（目录不可读或权限不足）:"
    printf '%s\n' "$SCAN_ERR"
    exit 1
fi
echo "  ✅ scan roots present and readable"

# ── 字符串字面量提取器 ─────────────────────────────────────────────────────────
# extract_imports <dir> <skip_tests:0|1>
#   只扫 Go 文件（--include='*.go'）：非 Go 文件里的字符串不该触发
#   「imports forbidden package」这种 BLOCKER。
#   输出 file:line:path（已去引号），因此 path 一定在行尾，可用 `$` 锚定。
#   关键点：**不要**用 `grep -v "// "` 过滤整行——那会把
#   `import _ ".../x" // 中文说明` 这种带行尾注释的真实违规整行丢掉。
#   本仓库中文行尾注释是常规写法，所以改为「先摘引号内容、再对内容匹配」，
#   行尾注释自然不再干扰匹配。
extract_imports() {
    local dir="$1" skip_tests="$2" raw
    raw="$(grep -rnoE --include='*.go' '"[^"]+"' "$dir" 2>/dev/null || true)"
    [ -z "$raw" ] && return 0
    if [ "$skip_tests" = "1" ]; then
        raw="$(printf '%s\n' "$raw" | grep -v '_test\.go:' || true)"
        [ -z "$raw" ] && return 0
    fi
    printf '%s\n' "$raw" | sed -E 's/^([^:]+:[0-9]+):"(.*)"$/\1:\2/'
}

# 规则 1–3 的匹配对象是被 import 的整条路径，可能带包名后缀也可能没有
# （`.../infrastructure/controller` 与 `.../infrastructure/controller/http/...` 都合法），
# 所以层段后面必须用 `(/|$)`：只写 `/` 会放过「层根包」这一种逃逸。
# 三个层根都是真实可 import 的包：domain/register.go、infrastructure/init.go、
# infrastructure/controller/register.go 分别声明 package domain / infrastructure / controller。

# 1. domain/ 禁止 import gin/gorm/redis/http/infrastructure/application
# 注意必须一并检查内部包全路径：只查 gin/gorm 这些短名会放过
# `domain/ → infrastructure/...`、`domain/ → application/...` 这类跨层违规。
echo "→ Checking domain/ import redlines..."
HITS="$(extract_imports domain 1 | grep -E 'gin-gonic/gin|gorm\.io/gorm|go-redis/v9|net/http|github\.com/PycMono/FastRAG/(infrastructure|application)(/|$)' || true)"
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
HITS="$(extract_imports application 1 | grep -E 'gin-gonic/gin|infrastructure/(driver|controller)(/|$)' || true)"
if [ -n "$HITS" ]; then
    printf '%s\n' "$HITS"
    echo "BLOCKER: application/ imports forbidden package (gin/infrastructure/driver/infrastructure/controller)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ application/ imports clean"
fi

# 3. common/ 禁止 import domain/application/infrastructure
echo "→ Checking common/ import redlines..."
HITS="$(extract_imports common 1 | grep -E 'github\.com/PycMono/FastRAG/(domain|application|infrastructure)(/|$)' || true)"
if [ -n "$HITS" ]; then
    printf '%s\n' "$HITS"
    echo "BLOCKER: common/ imports business layer (domain/application/infrastructure)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ common/ imports clean"
fi

# ── Controller 文件发现 ────────────────────────────────────────────────────────
# 用 find 而不是 glob：`infrastructure/controller/http/*/*.go` 只覆盖第 2 层，
# 会漏掉 depth-1 的 infrastructure/controller/http/register.go（它挂载了全部路由，
# 同样受「Controller 不得用 c.JSON」约束）。本机 /bin/bash 是 3.2.57，没有 globstar，
# 所以不能用 `**`。find 返回空时下面的零文件守卫会照常 BLOCKER。
find_controllers() {
    find infrastructure/controller/http -name '*.go' 2>/dev/null | sort || true
}

# 4. Controller 禁止直接返回 Entity（启发式：Controller 不应 import domain/entity）
echo "→ Checking Controller entity leakage..."
CTRL_FILES=0
CTRL_BAD=0
while IFS= read -r f; do
    [ -n "$f" ] || continue
    CTRL_FILES=$((CTRL_FILES + 1))
    if grep -q '"github\.com/PycMono/FastRAG/domain/entity' "$f" 2>/dev/null; then
        echo "MAJOR: Controller $f imports domain/entity — may return Entity instead of VO"
        ERRORS=$((ERRORS + 1))
        CTRL_BAD=$((CTRL_BAD + 1))
    fi
done < <(find_controllers)
if [ "$CTRL_FILES" -eq 0 ]; then
    echo "BLOCKER: infrastructure/controller/http 下未找到任何 .go 文件，无法检查"
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
#
# 规则 6 要回答的是「这个文件里有没有一处真实的 c.JSON( 调用」，
# 所以既不能把注释里的调用当成违规（误报），也不能让注释把真实调用藏起来（漏报）。
# 三条必须同时成立的性质：
#   (a) `c.JSON(200, nil) // 中文注释`      → 命中（行尾注释不能掩盖真实调用）
#   (b) `// c.JSON(200, nil)`               → 不命中（整行注释掉的调用不是违规）
#   (c) `s := "https://x/api"; c.JSON(200, s)` → 命中（字符串里的 // 不能截断整行）
# 朴素的 `sed 's|//.*$||'` 满足 (a)(b) 却违反 (c)：把字符串里的 // 当成注释起点，
# 会在真正调用之前就把该行截掉。这里改为逐字符扫描：跟踪块注释状态与字符串状态，
# 只在「不在字符串里」的位置把 `//` 当作注释起点，`/*` 当作块注释起点。
echo "→ Checking unified response format..."
FMT_FILES=0
FMT_BAD=0
while IFS= read -r f; do
    [ -n "$f" ] || continue
    FMT_FILES=$((FMT_FILES + 1))
    if [ "$(dirname "$f")" = "infrastructure/controller/http/health" ]; then
        echo "  ⏭️  Skipping $f (health probe needs fixed format)"
        continue
    fi
    FOUND="$(awk '
        {
            line = $0
            # 处于块注释中：整行都不算代码
            if (inblock) {
                if (line ~ /\*\//) { inblock = 0 }
                next
            }
            out = ""; instr = 0; i = 1; n = length(line)
            while (i <= n) {
                c = substr(line, i, 1)
                if (instr != 0) {
                    # 字符串内部：转义下一字符，遇到同种引号才结束
                    if (c == "\\") { out = out substr(line, i, 2); i += 2; continue }
                    if (c == instr) { instr = 0 }
                    out = out c; i++; continue
                }
                if (c == "\"" || c == "`") { instr = c; out = out c; i++; continue }
                if (c == "/" && substr(line, i, 2) == "//") { break }
                if (c == "/" && substr(line, i, 2) == "/*") {
                    rest = substr(line, i + 2)
                    if (match(rest, /\*\//)) { i = i + 2 + RSTART + 1; continue }
                    inblock = 1; break
                }
                out = out c; i++
            }
            if (out ~ /c\.JSON\(/) { print FILENAME ":" FNR ":" line }
        }
    ' "$f" 2>/dev/null || true)"
    if [ -n "$FOUND" ]; then
        printf '%s\n' "$FOUND"
        echo "BLOCKER: $f uses c.JSON() instead of gingext.Send()"
        ERRORS=$((ERRORS + 1))
        FMT_BAD=$((FMT_BAD + 1))
    fi
done < <(find_controllers)
if [ "$FMT_FILES" -eq 0 ]; then
    echo "BLOCKER: infrastructure/controller/http 下未找到任何 .go 文件，无法检查"
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
