"""Deterministic provider for tests and offline development."""

from __future__ import annotations

import hashlib

from model_providers.base import Completion, Message, ProviderConfig


class EchoProvider:
    """Returns the last user message, prefixed with a stable marker.

    The hash makes the response depend on the whole conversation, which keeps tests honest about ordering.
    """

    async def complete(self, messages: list[Message], config: ProviderConfig) -> Completion:
        last_user = next(
            (m.content for m in reversed(messages) if m.role == "user"),
            "",
        )
        digest = hashlib.sha256("\n".join(f"{m.role}:{m.content}" for m in messages).encode()).hexdigest()[:12]
        return Completion(
            text=f"[echo:{digest}] {last_user}",
            model=config.model,
            usage={"input_tokens": sum(len(m.content) for m in messages), "output_tokens": 0},
        )
