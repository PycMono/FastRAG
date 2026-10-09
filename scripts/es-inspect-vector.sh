#!/bin/bash
# 看 ES 里到底有没有向量 —— 排查用的小工具
#
# 用法:
#   bash scripts/es-inspect-vector.sh                       # 总数 + 抽一条切片（向量只打前 6 维）
#   bash scripts/es-inspect-vector.sh --full                # 加 --full：把 1024 个数字**全部**打出来
#   bash scripts/es-inspect-vector.sh <chunk_id>            # 看指定切片（chunk_id 就是 _id）
#   bash scripts/es-inspect-vector.sh --account <account>   # 只看某个租户
#
# 存在的理由：
#   ES 9 起 `index.mapping.exclude_source_vectors` **默认就是 true**（新建索引），
#   它在**存储层**就把 dense_vector 从 `_source` 里摘掉。于是你在 Kibana / _get /
#   _search 里翻 `_source`，`title_vec` / `content_vec` 压根不出现——
#   看起来就像"向量根本没写进去"，但其实它们好好地在 HNSW 图里、kNN 能召回。
#
#   本脚本走 `fields` API（官方推荐的那条路）把值取回来，证明向量在。
#
#   顺便提醒：**别为了"能在 _source 里看见"把 exclude_source_vectors 关掉**，
#   关掉之后 `_source` 会再存一份原始向量，磁盘每切片多约 8KB。排查就用这个脚本。
#
#   相关：设计文档 §4.2 的那个 ⚠️ 段落。

set -euo pipefail

# 仓库根由脚本自身位置解析，不依赖 $PWD
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="$ROOT/config.json"

if [[ ! -f "$CONFIG" ]]; then
  echo "找不到 $CONFIG" >&2
  exit 1
fi

# 从 config.json 取 ES 地址与索引名。
# 用 python3 而不是 jq：jq 不一定装，python3 在 macOS 上一定有。
read -r ES_URL INDEX < <(python3 - "$CONFIG" <<'PY'
import json, sys
c = json.load(open(sys.argv[1]))["elasticsearch"]
addr = (c.get("addrs") or ["http://127.0.0.1:9200"])[0].rstrip("/")
print(addr, c.get("index") or "fastrag")
PY
)

MODE="sample"
ARG=""
FULL="0"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --full)    FULL="1"; shift ;;
    --account) MODE="account"; ARG="${2:-}"; shift 2 ;;
    --chunk)   MODE="chunk";   ARG="${2:-}"; shift 2 ;;
    # 裸参数当 chunk_id 用（chunk_id 就是 _id，最常用的写法）
    *)         MODE="chunk";   ARG="$1"; shift ;;
  esac
done

if [[ "$MODE" != "sample" && -z "$ARG" ]]; then
  echo "缺参数。用法见文件头注释。" >&2
  exit 1
fi

# 先探一下 ES 在不在，免得后面拿一句语焉不详的 curl 错误去猜
if ! curl -sf -m 5 "$ES_URL/_cluster/health" > /dev/null; then
  echo "连不上 ES: $ES_URL" >&2
  echo "检查容器：docker ps | grep fastrag-es" >&2
  exit 1
fi

echo "ES   : $ES_URL"
echo "索引 : $INDEX"
echo

python3 - "$ES_URL" "$INDEX" "$MODE" "$ARG" "$FULL" <<'PY'
import json, sys, urllib.request

es, index, mode, arg = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
full = sys.argv[5] == "1"


def call(path, body):
    req = urllib.request.Request(
        f"{es}{path}",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


# ---- 全局概览：用 exists 聚合数一遍 ----
# 注意这里问的是 exists，不是"翻 _source 找不到就下结论"——
# 后者正是会把人带偏的那条路。
agg = call(f"/{index}/_search", {
    "size": 0,
    "aggs": {
        "has_content_vec": {"filter": {"exists": {"field": "content_vec"}}},
        "has_title_vec":   {"filter": {"exists": {"field": "title_vec"}}},
    },
})
total = agg["hits"]["total"]["value"]
nc = agg["aggregations"]["has_content_vec"]["doc_count"]
nt = agg["aggregations"]["has_title_vec"]["doc_count"]

print(f"切片总数        : {total}")
print(f"带 content_vec  : {nc}" + ("  ✅" if nc == total else "  ⚠️ 有切片缺向量"))
print(f"带 title_vec    : {nt}" + ("  ✅" if nt == total else "  ⚠️ 有切片缺向量"))

# ---- 挑一条切片，用 fields API 把向量取回来 ----
if mode == "sample":
    q, hint = {"match_all": {}}, "任意一条"
elif mode == "chunk":
    q, hint = {"term": {"_id": arg}}, f"_id = {arg}"
elif mode == "account":
    q, hint = {"term": {"account": arg}}, f"account = {arg}"
else:
    raise SystemExit(f"未知模式 {mode}")

res = call(f"/{index}/_search", {
    "size": 1,
    "_source": ["chunk_id", "doc_id", "title", "heading_path"],
    "fields": ["content_vec", "title_vec"],   # ← 关键：值不经过 _source
    "query": q,
})
hits = res["hits"]["hits"]
if not hits:
    print()
    print(f"没找到切片（{hint}）。")
    raise SystemExit(0)

h = hits[0]
src, fields = h["_source"], h.get("fields", {})
print()
print(f"取样切片（{hint}）")
print(f"  _id          : {h['_id']}")
print(f"  chunk_id     : {src.get('chunk_id')}")
print(f"  title        : {src.get('title')}")
print(f"  heading_path : {src.get('heading_path')}")
print(f"  _source 的键 : {sorted(src.keys())}")
print( "                 ↑ 这里**没有** *_vec —— 是 ES 9 的正常行为，不是丢了")

for name in ("content_vec", "title_vec"):
    v = fields.get(name)
    if not v:
        print(f"  {name:12} : ⚠️ 取不到")
        continue
    v = v[0] if isinstance(v[0], list) else v
    head = ", ".join(f"{x:.6f}" for x in v[:6])
    tail = "（全部见下）" if (full and name == "content_vec") else " ..."
    print(f"  {name:12} : {len(v)} 维  前6 = {head}{tail}")

# --full：把 content_vec 的 1024 个数字全打出来
if full:
    v = fields.get("content_vec")
    v = (v[0] if isinstance(v[0], list) else v) if v else None
    if v:
        print()
        print(f"content_vec 全部 {len(v)} 维：")
        for i in range(0, len(v), 8):
            print(f"  [{i:4d}] " + "  ".join(f"{x:>12.7f}" for x in v[i:i + 8]))
        print()
        print(f"  最小 {min(v):.7f}   最大 {max(v):.7f}   "
              f"非零 {sum(1 for x in v if x != 0)}/{len(v)}")

print()
print("想看整条向量的完整 JSON，把查询换成下面这个（_source.exclude_vectors=false）：")
print(f'''  curl -s "{es}/{index}/_search?pretty" -H 'Content-Type: application/json' -d '{{
    "_source": {{"exclude_vectors": false}},
    "size": 1,
    "query": {{"term": {{"_id": "{h['_id']}"}}}}
  }}' ''')

# 最后再证一次"向量真的能用来检索"——这才是向量存在的意义，
# 比"看得见"更接近真实诉求。
kn = call(f"/{index}/_search", {
    "size": 1,
    "fields": ["content_vec"],
    "query": {"term": {"_id": h["_id"]}},
})
vec = kn["hits"]["hits"][0]["fields"]["content_vec"]
vec = vec[0] if isinstance(vec[0], list) else vec
res2 = call(f"/{index}/_search", {
    "size": 1,
    "_source": ["title"],
    "knn": {"field": "content_vec", "query_vector": vec, "k": 1, "num_candidates": 50},
})
top = res2["hits"]["hits"][0]
print()
print("拿这条切片自己的向量去 kNN 搜它自己：")
print(f"  命中 _id = {top['_id']}  score = {top['_score']:.6f}")
print(f"  {'✅ 同一条，score≈1.0 —— 向量确实在 HNSW 图里' if top['_id'] == h['_id'] else '⚠️ 命中的不是它，值得查'}")
PY
