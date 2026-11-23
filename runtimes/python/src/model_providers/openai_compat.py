"""OpenAI-compatible chat completions over plain HTTP.

Compatible with OpenAI, vLLM, Ollama's OpenAI endpoint and most gateways. No vendor SDK is imported; the wire
format is stable and keeps the runtime image small.
"""

from __future__ import annotations

import httpx

from model_providers.base import Completion, Message, ProviderConfig, ProviderError


class OpenAICompatibleProvider:
    def __init__(self, client: httpx.AsyncClient | None = None) -> None:
        self._client = client

    async def complete(self, messages: list[Message], config: ProviderConfig) -> Completion:
        if not config.base_url:
            raise ProviderError("config", "base_url is required for an OpenAI-compatible provider")
        if config.api_key is None:
            raise ProviderError("config", "api_key is required for an OpenAI-compatible provider")

        body = {
            "model": config.model,
            "messages": [m.model_dump() for m in messages],
            "temperature": config.temperature,
            "max_tokens": config.max_tokens,
        }
        headers = {"Authorization": f"Bearer {config.api_key.get_secret_value()}"}
        url = f"{config.base_url.rstrip('/')}/chat/completions"

        try:
            if self._client is not None:
                response = await self._client.post(url, json=body, headers=headers)
            else:
                async with httpx.AsyncClient(timeout=config.timeout_seconds) as client:
                    response = await client.post(url, json=body, headers=headers)
        except httpx.TimeoutException as exc:
            raise ProviderError("timeout", "model provider timed out", retryable=True) from exc
        except httpx.HTTPError as exc:
            raise ProviderError("transport", "model provider request failed", retryable=True) from exc

        if response.status_code == 429 or response.status_code >= 500:
            raise ProviderError(
                "provider_unavailable",
                f"model provider returned {response.status_code}",
                retryable=True,
            )
        if response.status_code >= 400:
            raise ProviderError("provider_rejected", f"model provider returned {response.status_code}")

        try:
            data = response.json()
            text = data["choices"][0]["message"]["content"]
            usage = data.get("usage", {})
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            raise ProviderError("invalid_response", "model provider response was not understood") from exc

        return Completion(
            text=text,
            model=str(data.get("model", config.model)),
            usage={k: int(v) for k, v in usage.items() if isinstance(v, int)},
        )
