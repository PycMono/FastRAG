"""从切片前 MD 分层随机取证据，生成可追溯草稿；不读 ES 切片出题。"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import random
import re
import secrets


def blocks(path):
    raw = path.read_bytes()
    lines = raw.decode("utf-8-sig").splitlines()
    sections = []
    heading = "正文"
    start = 0
    for index, line in enumerate(lines):
        if re.match(r"^#{1,6}\s+", line):
            if index > start:
                sections.append((heading, start, index))
            heading = re.sub(r"^#{1,6}\s+", "", line).strip()
            start = index
    sections.append((heading, start, len(lines)))
    result = []
    for heading, begin, end in sections:
        if re.search(r"目录|版权|出版信息|参考文献|致谢|答案|译者序", heading):
            continue
        # 按自然段构建独立出题上下文，而非复用检索切片。
        cursor = begin
        while cursor < end:
            stop = min(cursor + 160, end)
            while stop > cursor + 5 and len("\n".join(lines[cursor:stop])) > 4800:
                stop -= 1
            text = "\n".join(lines[cursor:stop]).strip()
            if (
                400 <= len(text) <= 4800
                and len(re.findall(r"[\u4e00-\u9fff]", text)) > 150
            ):
                result.append(
                    {
                        "source": text,
                        "start_line": cursor + 1,
                        "end_line": stop,
                        "section": heading,
                        "position": cursor / max(1, len(lines)),
                    }
                )
            cursor = max(cursor + 1, stop)
    return raw, result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    parser.add_argument("--economics", required=True)
    parser.add_argument("--finance", required=True)
    parser.add_argument("--seed", type=int, default=20261126)
    args = parser.parse_args()
    rng = random.Random(args.seed)
    draft = {
        "id": "md-original-" + secrets.token_hex(6),
        "version": 1,
        "tenant": "home",
        "kb_no": "econ",
        "created_at": datetime.now(timezone.utc).isoformat(),
        "items": [],
    }
    report = {
        "seed": args.seed,
        "books": [],
        "sampling": "source-md / thirds-stratified-random / 10-easy-15-medium-25-hard-per-book",
    }
    for filename, doc_id in [
        (args.economics, "2123800461948489728"),
        (args.finance, "2125103608474112000"),
    ]:
        path = Path(filename)
        raw, candidates = blocks(path)
        file_hash = hashlib.sha256(raw).hexdigest()
        strata = [[], [], []]
        seen = set()
        for item in candidates:
            fingerprint = hashlib.sha256(
                " ".join(item["source"].split()).encode()
            ).hexdigest()
            if fingerprint in seen:
                continue
            seen.add(fingerprint)
            strata[min(2, int(item["position"] * 3))].append(item)
        selected = []
        for region, quota in enumerate([17, 17, 16]):
            pool = strata[region]
            if len(pool) < quota:
                raise RuntimeError(
                    f"{path.name} 第{region + 1}部分不足{quota}个有效片段，需人工调整采样"
                )
            # 随机章节轮询，避免同一章节挤满配额。
            chapters = {}
            for item in pool:
                chapters.setdefault(item["section"], []).append(item)
            keys = list(chapters)
            rng.shuffle(keys)
            for values in chapters.values():
                rng.shuffle(values)
            while quota:
                for key in keys:
                    if chapters[key] and quota:
                        item = chapters[key].pop()
                        selected.append(
                            {**item, "region": ["前部", "中部", "后部"][region]}
                        )
                        quota -= 1
        difficulties = ["简单"] * 10 + ["中等"] * 15 + ["困难"] * 25
        rng.shuffle(difficulties)
        for item, difficulty in zip(selected, difficulties):
            draft["items"].append(
                {
                    "id": f"q{len(draft['items']) + 1:03}",
                    "question": "",
                    "reference": "",
                    "doc_id": doc_id,
                    "doc_name": path.name,
                    "chunk_id": "",
                    "source": item["source"],
                    "source_hash": hashlib.sha256(item["source"].encode()).hexdigest(),
                    "source_type": "original_markdown",
                    "source_file": path.name,
                    "file_hash": file_hash,
                    "start_line": item["start_line"],
                    "end_line": item["end_line"],
                    "section": item["section"],
                    "region": item["region"],
                    "difficulty": difficulty,
                    "status": "draft",
                    "critical": False,
                }
            )
        report["books"].append(
            {
                "file": path.name,
                "sha256": file_hash,
                "eligible": len(candidates),
                "selected": 50,
                "front": 17,
                "middle": 17,
                "back": 16,
            }
        )
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    (output / (draft["id"] + ".json")).write_text(
        json.dumps(draft, ensure_ascii=False, indent=2)
    )
    (output / (draft["id"] + "-sampling.json")).write_text(
        json.dumps(report, ensure_ascii=False, indent=2)
    )
    print(json.dumps({"draft": draft["id"], **report}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
