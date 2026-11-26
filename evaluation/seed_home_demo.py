"""在 home 上执行：仅新增专用合成库，不读取/覆盖已有业务文档。"""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import urllib.request

ROOT = Path("/home/duomi/fastrag")
TENANT = "ragas-demo"
KB = "ragas-demo-20261125"
CASES = [
    ("密钥泄漏处置", "API Key 泄漏后需要做哪些处理？", "发现 API Key 泄漏后，立即吊销旧 Key，创建新 Key 并更新所有使用方，同时检查泄漏期间的调用记录。"),
    ("账号登录锁定", "连续输错密码会怎么样，多久恢复？", "账号连续输错密码 5 次后锁定 15 分钟；锁定期间不可登录，管理员可核验身份后提前解锁。"),
    ("数据备份", "业务数据多久备份一次，保留多少天？", "业务数据库每天凌晨 2 点进行全量备份，备份保留 30 天，每周执行一次恢复演练。"),
    ("文件上传限制", "上传文档允许哪些格式，最大多大？", "文档上传支持 PDF、DOCX 和 TXT，单个文件最大 20 MB；超过限制需要拆分后上传。"),
    ("退款规则", "购买后什么时候可以退款，到账要多久？", "购买后 7 天内且未使用服务可以申请退款；审核通过后原路退回，通常 3 个工作日到账。"),
    ("访问令牌有效期", "访问令牌和刷新令牌分别多久过期？", "访问令牌有效期为 2 小时，刷新令牌有效期为 30 天；刷新令牌失效后需要重新登录。"),
    ("生产变更审批", "上线生产环境需要谁批准，失败怎么办？", "生产变更必须通过负责人和运维双人审批，上线前准备回滚方案；验证失败立即回滚，并记录故障原因。"),
    ("客户数据删除", "用户要求删除数据时，主库和备份怎么处理？", "用户确认删除后，主库数据在 24 小时内删除；备份中的数据随 30 天保留周期自然过期，期间禁止用于业务恢复以外的用途。"),
    ("接口调用限流", "接口请求过于频繁时如何重试？", "接口按租户每分钟最多 60 次请求，超过返回 HTTP 429；调用方应按 Retry-After 指定秒数等待后重试，禁止立即循环重试。"),
    ("紧急告警响应", "一级告警需要多久响应，联系不上怎么办？", "一级告警要求值班人员在 5 分钟内确认，15 分钟内给出处置方案；无人确认时自动升级通知值班负责人。"),
]


def main():
    sql = (
        "INSERT INTO fastrag.knowledge (no,tenant,name,description,create_ts,update_ts) "
        f"VALUES ('{KB}','{TENANT}','Ragas 合成评测库','仅用于页面测试，不含业务资料',"
        "UNIX_TIMESTAMP()*1000,UNIX_TIMESTAMP()*1000);"
    )
    subprocess.run(
        ["docker", "exec", "-i", "fastrag-mysql", "sh", "-c",
         'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --default-character-set=utf8mb4'],
        input=sql.encode(), check=True,
    )
    config = json.loads((ROOT / "runtime/config.json").read_text())
    base = "http://127.0.0.1:" + str(config["http"]["port"])
    rows = []
    for number, (title, question, reference) in enumerate(CASES, 1):
        payload = {"tenant": TENANT, "kb_no": KB, "doc_name": title,
                   "format": "text", "content": title + "\n" + reference, "refresh": True}
        request = urllib.request.Request(base + "/api/v1/docs", data=json.dumps(payload).encode(),
                                         headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=120) as response:
            result = json.load(response)
        if result["code"] != 0:
            raise RuntimeError(f"导入失败：{title}")
        rows.append({"id": f"q{number:03}", "question": question, "tenant": TENANT,
                     "kb_nos": [KB], "category": "synthetic", "split": "dev",
                     "answerable": True, "critical": False, "reference": reference,
                     "reference_sources": [{"doc_id": str(result["data"]["doc_id"]), "heading_path": title}],
                     "reference_context_ids": None})
        print(f"Imported {number}/{len(CASES)}: {title}", flush=True)
    directory = ROOT / "runtime/evaluation/datasets/home-demo"
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    manifest = {"dataset_version": "home-demo-v1", "annotation_version": "synthetic-a1",
                "corpus_snapshot": KB, "corpus_hash": hashlib.sha256(json.dumps(CASES, ensure_ascii=False).encode()).hexdigest(),
                "retrieval_settings": config["search"]}
    profiles = [{"name": "hybrid", "limit": 3, "retrieve_count": 6, "dense_weight": 0.5,
                 "rerank_switch": False, "embed_model": "", "rerank_model": "",
                 "min_score": 0, "fusion_min_score": 0}]
    for name, value in [("manifest.json", manifest), ("profiles.json", profiles)]:
        (directory / name).write_text(json.dumps(value, ensure_ascii=False, indent=2))
    (directory / "cases.jsonl").write_text("\n".join(json.dumps(row, ensure_ascii=False) for row in rows) + "\n")
    for path in directory.iterdir():
        os.chmod(path, 0o600)
    print("Dataset ready: home-demo (10 synthetic cases, top-3, no rerank)")


if __name__ == "__main__":
    main()
