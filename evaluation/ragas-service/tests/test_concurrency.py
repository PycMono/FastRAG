import asyncio
from types import SimpleNamespace

from fastapi.testclient import TestClient
from app import main


def test_generation_does_not_block_scoring(monkeypatch):
    monkeypatch.setenv("EVAL_SERVICE_TOKEN", "x" * 32)
    monkeypatch.setenv("JUDGE_MODEL", "test-model")
    monkeypatch.setenv("JUDGE_API_KEY", "test-key")
    monkeypatch.setattr(main, "llm_factory", lambda *args, **kwargs: SimpleNamespace())
    with TestClient(main.app) as client:
        # 模拟后台生成占用其配额，评分使用另一个独立配额。
        client.portal.call(main.app.state.generation_semaphore.acquire)
        response = client.post(
            "/evaluate",
            json={
                "request_id": "concurrent",
                "dataset_version": "v1",
                "annotation_version": "a1",
                "case_id": "q1",
                "user_input": "问题",
                "reference": "参考",
                "retrieved_contexts": [],
                "retrieved_context_ids": [],
                "metrics": ["ContextRecall"],
            },
            headers={"Authorization": "Bearer " + "x" * 32},
        )
        assert response.status_code == 200
        assert response.json()["metrics"][0]["value"] == 0
