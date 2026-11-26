#!/usr/bin/env bash
#
# 建 FastRAG 的 ES 索引。索引**不由服务创建**（设计文档 §9.4）——上线前跑一次这个。
#
#   bash deploy/create-es-index.sh
#
# 索引已存在时 ES 会返回 400 + resource_already_exists_exception；
# 这不是部署失败，保留已有 mapping，不覆盖。
response=$(mktemp)
trap 'rm -f "$response"' EXIT

status=$(curl -sS -o "$response" -w '%{http_code}' -XPUT "${ES_URL%/}/fastrag" \
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
      "tenant":      { "type": "keyword" },
      "kb_id":        { "type": "long" },
      "doc_id":       { "type": "long" },
      "chunk_id":     { "type": "keyword" },
      "order":        { "type": "integer" },
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
)

if [ "$status" = "200" ] || [ "$status" = "201" ]; then
	cat "$response"
	printf '\n索引已就绪：%s/fastrag\n' "${ES_URL%/}"
	exit 0
fi

if grep -q 'resource_already_exists_exception' "$response"; then
	printf '索引已存在，保留现有 mapping，未覆盖：%s/fastrag\n' "${ES_URL%/}"
	exit 0
fi

printf '建索引失败 HTTP %s：%s\n' "$status" "$(cat "$response")" >&2
exit 1
