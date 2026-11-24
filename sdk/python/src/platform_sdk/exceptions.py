"""SDK exception hierarchy."""

from __future__ import annotations

from typing import Any


class PlatformError(Exception):
    """Base class for every SDK error."""


class ConfigurationError(PlatformError):
    """The client is missing a required base URL, API key or scope."""


class ConnectionError(PlatformError):
    """The API could not be reached."""


class PlatformTimeoutError(PlatformError):
    """The API call or execution wait timed out."""


class APIError(PlatformError):
    def __init__(
        self,
        status_code: int,
        code: str = "api_error",
        message: str = "",
        *,
        details: Any = None,
        request_id: str | None = None,
    ) -> None:
        super().__init__(f"{status_code} {code}: {message}")
        self.status_code = status_code
        self.code = code
        self.message = message
        self.details = details
        self.request_id = request_id


class AuthenticationError(APIError):
    pass


class PermissionDeniedError(APIError):
    pass


class NotFoundError(APIError):
    pass


class ConflictError(APIError):
    pass


class ValidationError(APIError):
    pass


class RateLimitError(APIError):
    def __init__(self, *args: Any, retry_after: float | None = None, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.retry_after = retry_after


class ServerError(APIError):
    pass


def from_response(
    status_code: int,
    code: str,
    message: str,
    *,
    details: Any = None,
    request_id: str | None = None,
    retry_after: float | None = None,
) -> APIError:
    common = {"details": details, "request_id": request_id}
    if status_code == 401:
        return AuthenticationError(status_code, code, message, **common)
    if status_code == 403:
        return PermissionDeniedError(status_code, code, message, **common)
    if status_code == 404:
        return NotFoundError(status_code, code, message, **common)
    if status_code == 409:
        return ConflictError(status_code, code, message, **common)
    if status_code == 422:
        return ValidationError(status_code, code, message, **common)
    if status_code == 429:
        return RateLimitError(status_code, code, message, retry_after=retry_after, **common)
    if status_code >= 500:
        return ServerError(status_code, code, message, **common)
    return APIError(status_code, code, message, **common)
