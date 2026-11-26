import asyncio
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient

from app import main


def sample(contexts=None):
    values = [] if contexts is None else contexts
    return main.EvaluationInput(
        request_id="r1",
        dataset_version="v1",
        annotation_version="a1",
        case_id="q1",
        user_input="问题",
        reference="标准证据",
        retrieved_contexts=values,
        retrieved_context_ids=[f"c{i}" for i in range(len(values))],
        metrics=["ContextRecall"],
    )


def test_empty_recall_is_policy_zero():
    result = asyncio.run(main.metric_result(None, "ContextRecall", sample(), 1))
    assert result["status"] == "ok"
    assert result["value"] == 0
    assert result["computed_by"] == "policy"


def test_missing_reference_is_not_applicable():
    data = sample()
    data.reference = ""
    result = asyncio.run(main.metric_result(None, "ContextRecall", data, 1))
    assert result["status"] == "not_applicable"
    assert result["value"] is None


@pytest.mark.skipif(
    main.IDBasedContextRecall is None, reason="ragas lacks id-based recall"
)
def test_id_based_recall_skips_model():
    data = sample(["证据"])
    data.reference_context_ids = ["c0"]
    result = asyncio.run(main.metric_result(None, "ContextRecall", data, 1))
    assert result["status"] == "ok"
    assert result["computed_by"] == "id_based"
    assert result["value"] == 1


@pytest.mark.skipif(
    main.IDBasedContextPrecision is None, reason="ragas lacks id-based precision"
)
def test_id_based_precision_skips_model():
    data = sample(["证据一", "证据二"])
    data.reference_context_ids = ["c1"]
    result = asyncio.run(main.metric_result(None, "ContextPrecision", data, 1))
    assert result["status"] == "ok"
    assert result["computed_by"] == "id_based"
    assert 0 <= result["value"] <= 1


def test_precision_retries_only_failed_context(monkeypatch):
    attempts = {}

    class Flaky:
        def __init__(self, llm):
            pass

        async def ascore(self, **kwargs):
            context = kwargs["retrieved_contexts"][0]
            attempts[context] = attempts.get(context, 0) + 1
            if context == "坏" and attempts[context] == 1:
                raise RuntimeError("truncated")
            return SimpleNamespace(value=1.0 if context == "好" else 0.0)

    monkeypatch.setattr(main, "ContextPrecision", Flaky)
    result = asyncio.run(
        main.metric_result(None, "ContextPrecision", sample(["好", "坏"]), 2)
    )
    assert result["status"] == "ok"
    assert result["value"] == pytest.approx(1)
    assert attempts == {"好": 1, "坏": 2}


def test_metric_failure_does_not_leak_context(monkeypatch):
    class Broken:
        def __init__(self, **kwargs):
            pass

        async def ascore(self, **kwargs):
            raise ValueError("private business context and token")

    monkeypatch.setattr(main, "ContextRecall", Broken)
    result = asyncio.run(main.metric_result(None, "ContextRecall", sample(["证据"]), 1))
    assert result["status"] == "error"
    assert "private" not in result["error"]


@pytest.fixture
def client(monkeypatch):
    monkeypatch.setenv("EVAL_SERVICE_TOKEN", "x" * 32)
    monkeypatch.setenv("JUDGE_MODEL", "test-model")
    monkeypatch.setenv("JUDGE_API_KEY", "test-key")
    monkeypatch.setattr(main, "llm_factory", lambda *args, **kwargs: SimpleNamespace())
    with TestClient(main.app) as value:
        yield value


def test_api_auth_and_empty_result(client):
    body = sample().model_dump()
    assert client.post("/evaluate", json=body).status_code == 401
    response = client.post(
        "/evaluate", json=body, headers={"Authorization": "Bearer " + "x" * 32}
    )
    assert response.status_code == 200
    assert response.json()["metrics"][0]["value"] == 0


def test_api_rejects_unknown_metrics(client):
    body = sample().model_dump()
    body["metrics"] = ["Faithfulness"]
    assert (
        client.post(
            "/evaluate", json=body, headers={"Authorization": "Bearer " + "x" * 32}
        ).status_code
        == 422
    )


def test_real_ragas_metrics_with_fake_judge():
    """执行真实指标算法，只替换模型，不产生收费调用。"""
    from ragas.llms import llm_factory

    class Judge:
        async def agenerate(self, prompt, response_model):
            name = response_model.__name__
            if name == "ContextPrecisionOutput":
                return response_model.model_validate(
                    {"reason": "supported", "verdict": 1}
                )
            if name == "ContextRecallOutput":
                return response_model.model_validate(
                    {
                        "classifications": [
                            {
                                "statement": "fact",
                                "reason": "supported",
                                "attributed": 1,
                            }
                        ]
                    }
                )
            return response_model.model_validate({"rating": 2})

    async def run():
        # 真实模型包装器用于满足指标构造校验，然后替换 agenerate。
        client = main.AsyncOpenAI(api_key="test-key")
        try:
            llm = llm_factory("test-model", client=client, max_retries=1)
            llm.agenerate = Judge().agenerate
            results = [
                await main.metric_result(
                    llm, name, sample(["检索返回的标准证据内容"]), 2
                )
                for name in ["ContextPrecision", "ContextRecall", "ContextRelevance"]
            ]
            assert all(value["status"] == "ok" for value in results), results
            assert all(value["value"] > 0.99 for value in results), results
        finally:
            await client.close()

    asyncio.run(run())


def test_batch_id_metrics_do_not_need_model(client):
    body = {
        "judge": {
            "base_url": "https://api.minimaxi.com/v1",
            "model": "dummy",
            "api_key": "x" * 24,
        },
        "samples": [
            {
                "request_id": "b1",
                "dataset_version": "v1",
                "annotation_version": "a1",
                "case_id": "q1",
                "user_input": "问题",
                "reference": "答案",
                "retrieved_contexts": ["证据一", "证据二"],
                "retrieved_context_ids": ["c1", "c2"],
                "reference_context_ids": ["c2"],
                "metrics": ["ContextPrecision", "ContextRecall"],
            }
        ],
    }
    response = client.post(
        "/evaluate-batch",
        json=body,
        headers={"Authorization": "Bearer " + "x" * 32},
    )
    assert response.status_code == 200
    result = response.json()["results"][0]
    assert result["overall_status"] == "succeeded"
    metrics = {item["name"]: item for item in result["metrics"]}
    assert metrics["ContextPrecision"]["computed_by"] == "id_based"
    assert metrics["ContextRecall"]["computed_by"] == "id_based"
    assert metrics["ContextRecall"]["value"] == 1
