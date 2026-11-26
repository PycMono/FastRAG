"""在部署宿主机为已有草稿出题；逐题保存，不覆盖已有题、不自动复核。"""

import argparse
import json
from pathlib import Path
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True)
    parser.add_argument("--draft", required=True)
    parser.add_argument("--base-url", default="http://127.0.0.1:8088")
    args = parser.parse_args()
    root = Path(args.root)
    environment = dict(
        line.split("=", 1)
        for line in (root / ".admin.env").read_text().splitlines()
        if "=" in line
    )
    headers = {
        "Authorization": "Bearer " + environment["FASTRAG_ADMIN_TOKEN"],
        "Content-Type": "application/json",
    }

    def call(path, body=None):
        request = urllib.request.Request(
            args.base_url.rstrip("/") + path,
            data=None if body is None else json.dumps(body).encode(),
            headers=headers,
        )
        with urllib.request.urlopen(request, timeout=110) as response:
            result = json.load(response)
        if result["code"] != 0:
            raise RuntimeError("generation rejected: " + str(result["code"]))
        return result["data"]

    failures = []
    drafts = call("/api/admin/eval/drafts")
    original = next(draft for draft in drafts if draft["id"] == args.draft)
    for index, candidate in enumerate(original["items"], 1):
        # 页面可能同时编辑，生成前重新读取版本；服务端再次检查冲突。
        draft = next(value for value in call("/api/admin/eval/drafts") if value["id"] == args.draft)
        current = next(value for value in draft["items"] if value["id"] == candidate["id"])
        if current["question"] or current["status"] != "draft":
            continue
        try:
            call("/api/admin/eval/drafts/" + args.draft + "/generate", {
                "case_id": current["id"], "version": draft["version"],
            })
            print(f"{index}/{len(original['items'])} {current['id']} saved", flush=True)
        except (urllib.error.URLError, RuntimeError, TimeoutError) as error:
            failures.append(current["id"])
            print(f"{index}/{len(original['items'])} {current['id']} failed ({type(error).__name__}); no retry", flush=True)
    print(json.dumps({"finished": True, "failed_ids": failures}), flush=True)


if __name__ == "__main__":
    main()
