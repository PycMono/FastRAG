#!/usr/bin/env bash
#
# 建 FastRAG 的 ES 索引。索引**不由服务创建**（设计文档 §9.4）——上线前跑一次这个。
#
#   bash scripts/create-es-index.sh
#
# 下面就是一条普通的 curl，可以直接抄进终端执行（改掉 ES 地址即可）。
#
# 已经建过的话，ES 会返回 resource_already_exists_exception，**不会覆盖** ——这正是想要的。
# 要重建得先自己删（删索引 = 丢全部切片，所以不代劳）：
#   curl -XDELETE 'http://127.0.0.1:9200/fastrag'
#
# ⚠️ 字段名的另一半在 infrastructure/persistence/knowledge/field.go（查询/写入用的常量）。
#    改一边就要改另一边：对不上不会报错，只会静默查不到结果。
#
# ─── 字段为什么这么定（改之前先读） ────────────────────────────────────────────
#
# · account / biz_tag / chunk_id 必须是 keyword，不能是 text。
#   account 是**单索引多租户下唯一的隔离手段**（D2），隔离靠 term 查询打在它身上。
#   一旦它变成 text，term 会按分词匹配，「demo」就能命中「demo-other」的文档 ——
#   那是跨租户越权，不是召回变小。biz_tag（租户内再分域、过滤用）、
#   chunk_id（主键语义，也是 _id 的来源）同理。
#
# · title / content / heading_path 是 text + IK。heading_path 是标题链，
#   由结构感知切片产生，相当于廉价版的 contextual retrieval。
#
# · title_vec / content_vec 是 dense_vector，**刻意不写 index_options**：
#   ES 9 会默认用 bbq_hnsw（二值量化 HNSW），图的内存占用降到约 1/4，
#   代价是召回略损 —— ES 用 rescore_vector.oversample=3.0 补回来一部分。
#   若实测召回不达标，再显式覆盖成纯 hnsw：
#     "index_options": { "type": "hnsw", "m": 16, "ef_construction": 100 }
#
# ⚠️ dims 建好之后**改不了**。换 embedding 模型 = 新建索引 + 全量重导，
#    本期没有影索引机制，代价是停写重导（设计文档 §9.4）。
#    这个 1024 必须与 embedding 模型实际输出的维度一致（当前模型 bge-m3 = 1024）。
#    config.json 的 embedding.dim 填了的话，服务写入前会拦一道；留空则不校验。
#
# · 分片数要按**全量**语料估（不是按租户），随数据量调整（§9.2）。
#   副本数单节点本地必须是 0 —— 副本分片无处分配会让集群一直 yellow。

set -euo pipefail

# 下面就是一条普通的 curl，可以原样抄进终端执行，也可以直接交给运维。
#
# --fail-with-body：HTTP 出错时不但退出码非零（set -e 就此打住），
# 还把 ES 的 error 体原样打出来。光用 -f 的话只会剩一句 "returned error: 400"，
# 而真正的原因（哪个 analyzer 没装、哪个 dims 不对）写在 body 里。
curl -sS --fail-with-body -XPUT 'http://127.0.0.1:9200/fastrag' \
  -H 'Content-Type: application/json' \
  -d @- <<'JSON'
{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "refresh_interval": "30s",
    "analysis": {
      "analyzer": {
        "ik_max_word": { "type": "custom", "tokenizer": "ik_max_word" },
        "ik_smart":    { "type": "custom", "tokenizer": "ik_smart" }
      }
    }
  },
  "mappings": {
    "properties": {
      "account":      { "type": "keyword" },
      "kb_id":        { "type": "long" },
      "doc_id":       { "type": "long" },
      "chunk_id":     { "type": "keyword" },
      "order":        { "type": "integer" },
      "biz_tag":      { "type": "keyword" },
      "title":        { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
      "content":      { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
      "heading_path": { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
      "title_vec":    { "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine" },
      "content_vec":  { "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine" },
      "create_ts":    { "type": "long" }
    }
  }
}
JSON

printf '\n索引已就绪：http://127.0.0.1:9200/fastrag\n'
