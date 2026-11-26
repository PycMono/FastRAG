"""远端首次部署时补齐 .admin.env / config.json / ragas/.env。

只做“缺什么补什么”，不覆盖已有文件，也不打印任何密钥。
由 deploy/deploy.sh bootstrap 调用。
"""

import json
import os
import secrets
import sys


def read_env(path):
    result = {}
    if not os.path.exists(path):
        return result
    for line in open(path):
        line = line.strip()
        if "=" in line and not line.startswith("#"):
            key, value = line.split("=", 1)
            result[key] = value
    return result


def ensure_admin_env():
    if os.path.exists(".admin.env"):
        return
    password = ""
    if os.path.exists("config.json"):
        try:
            password = json.load(open("config.json")).get("mysql", {}).get("password", "")
        except Exception:
            pass
    if not password:
        password = secrets.token_hex(16)
    lines = [
        "MYSQL_ROOT_PASSWORD=" + password,
        "FASTRAG_ADMIN_TOKEN=" + secrets.token_hex(24),
        "FASTRAG_EVAL_TOKEN=" + secrets.token_hex(24),
    ]
    with open(".admin.env", "w") as file:
        file.write("\n".join(lines) + "\n")
    os.chmod(".admin.env", 0o600)


def ensure_config():
    if os.path.exists("config.json"):
        return
    env = read_env(".admin.env")
    config = json.load(open("config.json.bootstrap"))
    config["mysql"].update(
        {
            "host": "127.0.0.1",
            "port": 3306,
            "database": "fastrag",
            "user": "root",
            "password": env.get("MYSQL_ROOT_PASSWORD", ""),
        }
    )
    config["http"]["host"] = "0.0.0.0"
    config["http"]["port"] = "8088"
    config["embedding"]["default"] = "bge-m3"
    config["rerank"]["enabled"] = False
    config["evaluation"]["enabled"] = False
    config["security"]["trust_request_tenant"] = True
    with open("config.json", "w") as file:
        json.dump(config, file, ensure_ascii=False, indent=2)
        file.write("\n")
    os.chmod("config.json", 0o600)


def ensure_mysql_env():
    if os.path.exists(".env"):
        return
    password = read_env(".admin.env").get("MYSQL_ROOT_PASSWORD", "")
    if not password:
        raise SystemExit("MYSQL_ROOT_PASSWORD 缺失")
    with open(".env", "w") as file:
        file.write("MYSQL_ROOT_PASSWORD=" + password + "\n")
    os.chmod(".env", 0o600)


def ensure_ragas_env():
    if os.path.exists("ragas/.env"):
        return
    token = read_env(".admin.env").get("FASTRAG_EVAL_TOKEN", "")
    if len(token) < 24:
        raise SystemExit("FASTRAG_EVAL_TOKEN 缺失或过短")
    os.makedirs("ragas", exist_ok=True)
    with open("ragas/.env", "w") as file:
        file.write("EVAL_SERVICE_TOKEN=" + token + "\n")
    os.chmod("ragas/.env", 0o600)


if __name__ == "__main__":
    action = sys.argv[1] if len(sys.argv) > 1 else ""
    if action == "admin":
        ensure_admin_env()
    elif action == "config":
        ensure_config()
    elif action == "mysql":
        ensure_mysql_env()
    elif action == "ragas":
        ensure_ragas_env()
    else:
        raise SystemExit("usage: bootstrap_env.py admin|config|mysql|ragas")
