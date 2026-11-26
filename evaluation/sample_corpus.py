"""全量读取已有文档/切片，按文档和章节分层取样；不写 ES，不调用模型。

在 FastRAG 宿主机执行，目录参数指向其 runtime。输出草稿可由复核页读取。
"""

import argparse
from collections import Counter, defaultdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import urllib.parse
import urllib.request


def select_evenly(items, count):
    if count >= len(items):
        return list(items)
    return [
        items[min(len(items) - 1, int((index + 0.5) * len(items) / count))]
        for index in range(count)
    ]


def sample(groups, count):
    """先按文档轮询分配额度，再按章节轮询分配，章节内部等距选位置。"""
    quotas = Counter()
    while sum(quotas.values()) < count:
        changed = False
        for key, items in groups.items():
            if quotas[key] < len(items) and sum(quotas.values()) < count:
                quotas[key] += 1
                changed = True
        if not changed:
            break
    selected = []
    for key, items in groups.items():
        chapters = defaultdict(list)
        for item in items:
            chapters[item.get("heading_path") or "[无章节标记]"].append(item)
        # 章节数量过多时，也等距选择章节，不偏向书籍开头。
        chapter_names = select_evenly(list(chapters), min(quotas[key], len(chapters)))
        chapter_quotas = Counter()
        while sum(chapter_quotas.values()) < quotas[key]:
            changed = False
            for name in chapter_names:
                if (
                    chapter_quotas[name] < len(chapters[name])
                    and sum(chapter_quotas.values()) < quotas[key]
                ):
                    chapter_quotas[name] += 1
                    changed = True
            if not changed:
                break
        for name in chapter_names:
            selected.extend(select_evenly(chapters[name], chapter_quotas[name]))
    return selected


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime", required=True)
    parser.add_argument("--base-url", default="http://127.0.0.1:8088")
    parser.add_argument("--tenant", required=True)
    parser.add_argument("--kb", required=True)
    parser.add_argument("--count", type=int, default=150)
    args = parser.parse_args()
    if not 1 <= args.count <= 1000:
        parser.error("count 必须为 1–1000")

    def get(path, params):
        url = args.base_url.rstrip("/") + path + "?" + urllib.parse.urlencode(params)
        with urllib.request.urlopen(url, timeout=60) as response:
            body = json.load(response)
        if body.get("code") != 0:
            raise RuntimeError("读取语料失败，不保存不完整样本")
        return body["data"]

    documents = []
    page = 1
    while True:
        data = get(
            "/api/v1/docs",
            {"tenant": args.tenant, "kb_no": args.kb, "page": page, "page_size": 100},
        )
        documents.extend(data["list"])
        if len(documents) >= data["total"]:
            break
        if not data["list"]:
            raise RuntimeError("文档分页提前结束")
        page += 1

    groups = {}
    seen = set()
    statistics = []
    inventory = []
    for document in documents:
        chunks = []
        offset = 0
        while True:
            data = get(
                "/api/v1/docs/chunks",
                {
                    "tenant": args.tenant,
                    "doc_id": document["doc_id"],
                    "limit": 200,
                    "offset": offset,
                },
            )
            chunks.extend(data["items"])
            if len(chunks) >= data["total"]:
                break
            if not data["items"]:
                raise RuntimeError("切片分页提前结束")
            offset += len(data["items"])
        eligible = []
        duplicates = 0
        for chunk in sorted(
            chunks, key=lambda value: (value["order"], value["chunk_id"])
        ):
            normalized = " ".join(chunk["content"].split())
            fingerprint = hashlib.sha256(normalized.encode()).hexdigest()
            inventory.append(
                (
                    document["doc_id"],
                    chunk["chunk_id"],
                    hashlib.sha256(chunk["content"].encode()).hexdigest(),
                )
            )
            if not 100 <= len(chunk["content"]) <= 6000:
                continue
            if fingerprint in seen:
                duplicates += 1
                continue
            seen.add(fingerprint)
            eligible.append(
                {
                    **chunk,
                    "doc_id": document["doc_id"],
                    "doc_name": document["doc_name"],
                }
            )
        groups[document["doc_id"]] = eligible
        statistics.append(
            {
                "doc_id": document["doc_id"],
                "doc_name": document["doc_name"],
                "scanned": len(chunks),
                "eligible": len(eligible),
                "duplicates": duplicates,
                "chapters_available": len(
                    {value.get("heading_path") or "[无章节标记]" for value in eligible}
                ),
            }
        )

    selected = sample(groups, args.count)
    draft_id = "sample-" + secrets.token_hex(8)
    draft = {
        "id": draft_id,
        "version": 1,
        "tenant": args.tenant,
        "kb_no": args.kb,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "items": [],
    }
    for index, item in enumerate(selected, 1):
        draft["items"].append(
            {
                "id": f"q{index:03}",
                "question": "",
                "reference": "",
                "doc_id": item["doc_id"],
                "doc_name": item["doc_name"],
                "chunk_id": item["chunk_id"],
                "source": item["content"],
                "source_hash": hashlib.sha256(item["content"].encode()).hexdigest(),
                "status": "draft",
                "critical": False,
            }
        )
    for entry in statistics:
        values = [item for item in selected if item["doc_id"] == entry["doc_id"]]
        entry["sampled"] = len(values)
        entry["chapters_sampled"] = len(
            {item.get("heading_path") or "[无章节标记]" for item in values}
        )
        entry["min_order"] = min((item["order"] for item in values), default=None)
        entry["max_order"] = max((item["order"] for item in values), default=None)
    report = {
        "draft_id": draft_id,
        "requested": args.count,
        "sampled": len(selected),
        "scanned": len(inventory),
        "documents": statistics,
        "inventory_hash": hashlib.sha256(
            json.dumps(sorted(inventory)).encode()
        ).hexdigest(),
        "method": "document-balanced / heading-stratified / evenly-spaced / normalized-exact-dedup",
        "limitations": [
            "章节依据现有 heading_path；缺失章节只能按位置分散",
            "仅原文采样，尚未出题或人工复核",
            "取样期间语料需保持不变",
        ],
    }
    root = Path(args.runtime) / "evaluation/runs"
    for subdir, name, value in [
        ("drafts", draft_id, draft),
        ("sampling", draft_id, report),
    ]:
        directory = root / subdir
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        path = directory / (name + ".json")
        with path.open("x") as output:
            os.chmod(path, 0o600)
            json.dump(value, output, ensure_ascii=False, indent=2)
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
