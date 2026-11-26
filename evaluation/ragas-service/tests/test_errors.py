from app.main import classify_error


def test_classification_does_not_expose_exception_text():
    error = ValueError("api_key=secret; private prompt")
    message, details = classify_error(error)
    assert "secret" not in message
    assert "private" not in str(details)
    assert details["category"] == "invalid_value"


def test_nested_provider_error_has_safe_status():
    class ProviderError(Exception):
        status_code = 429
        request_id = "request-123"

    wrapped = RuntimeError("private context")
    wrapped.__cause__ = ProviderError("secret")
    message, details = classify_error(wrapped)
    assert details["category"] == "rate_limit"
    assert details["http_status"] == 429
    assert details["provider_request_id"] == "request-123"
    assert "secret" not in message


def test_structured_output_error_is_not_authentication():
    class InstructorRetryException(Exception):
        pass

    _, details = classify_error(InstructorRetryException("private completion"))
    assert details["stage"] == "structured_output"
    assert details["category"] == "invalid_output"
