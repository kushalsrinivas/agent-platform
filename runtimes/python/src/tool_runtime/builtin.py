"""Built-in tools available to every workflow.

They are deterministic and side-effect free, which makes them safe under at-least-once delivery.
"""

from __future__ import annotations

import asyncio
import html
import ipaddress
import os
import re
import socket
from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, ClassVar
from urllib.parse import urljoin, urlsplit
from xml.etree import ElementTree

import httpx

_FEED_MAX_BYTES = 2 * 1024 * 1024
_FEED_MAX_ITEMS = 20
_FEED_SUMMARY_CHARS = 400
_FEED_MAX_REDIRECTS = 3
_FEED_SCHEMES = {"http", "https"}
_TAG_PATTERN = re.compile(r"<[^>]+>")
_WHITESPACE_PATTERN = re.compile(r"\s+")


def _record_tool_call(path: str, idempotency_key: str) -> None:
    with Path(path).open("a", encoding="utf-8") as handle:
        handle.write(f"{idempotency_key}\n")


class EchoTool:
    name = "echo"
    version = "1"

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        del idempotency_key
        return {"echo": arguments}


class WebSearchTool:
    """Deterministic stand-in for the platform web-search tool.

    Phase 1 has no network egress from the sandbox; the control-plane workflow references the tool by name and
    version, and the runtime returns a stable, inspectable result.
    """

    name = "tool_web_search"
    version = "1"

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        del idempotency_key
        query = arguments.get("query") or arguments.get("input") or ""
        return {
            "query": str(query),
            "results": [
                {"title": f"Result for {query}", "url": "https://example.invalid/result"},
            ],
        }


class AddTool:
    name = "add"
    version = "1"

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        counter = os.environ.get("AGENT_RUNTIME_TOOL_COUNTER_FILE", "").strip()
        if counter:
            await asyncio.to_thread(_record_tool_call, counter, idempotency_key)
        numbers = arguments.get("numbers")
        if not isinstance(numbers, list) or not numbers:
            raise ValueError("numbers must be a non-empty list")
        if not all(isinstance(n, int | float) and not isinstance(n, bool) for n in numbers):
            raise ValueError("numbers must contain only numbers")
        return {"sum": sum(numbers)}


def _local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1].lower()


def _child(node: ElementTree.Element, name: str) -> ElementTree.Element | None:
    wanted = name.lower()
    for child in node:
        if _local_name(child.tag) == wanted:
            return child
    return None


def _text(node: ElementTree.Element | None) -> str:
    if node is None:
        return ""
    return "".join(node.itertext()).strip()


def _clean(value: str) -> str:
    return _WHITESPACE_PATTERN.sub(" ", html.unescape(_TAG_PATTERN.sub(" ", value))).strip()


def _validate_public_url(url: str) -> None:
    """Reject non-HTTP URLs and hosts that resolve to private, loopback or link-local addresses."""
    parts = urlsplit(url)
    if parts.scheme not in _FEED_SCHEMES:
        raise ValueError("only http and https URLs are supported")
    host = parts.hostname
    if not host:
        raise ValueError("URL must include a host")
    port = parts.port or (443 if parts.scheme == "https" else 80)
    try:
        addresses = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except OSError as exc:
        raise ValueError(f"host {host!r} could not be resolved") from exc
    for address in addresses:
        try:
            ip = ipaddress.ip_address(str(address[4][0]).split("%", 1)[0])
        except ValueError as exc:
            raise ValueError("URL host did not resolve to an IP address") from exc
        # shortcut: httpx re-resolves DNS after this check, so a rebinding resolver could still reach a private IP.
        # Upgrade by pinning the validated address when this tool is exposed to untrusted URLs.
        if (
            ip.is_private
            or ip.is_loopback
            or ip.is_link_local
            or ip.is_multicast
            or ip.is_reserved
            or ip.is_unspecified
        ):
            raise ValueError("URL resolves to a non-public address")


def _rss_item(node: ElementTree.Element, feed_url: str) -> dict[str, str]:
    link = _text(_child(node, "link")) or _text(_child(node, "guid"))
    summary = ""
    for name in ("description", "summary", "encoded", "content"):
        summary = _text(_child(node, name))
        if summary:
            break
    return {
        "title": _clean(_text(_child(node, "title"))),
        "url": urljoin(feed_url, link),
        "published": _clean(_text(_child(node, "pubDate")) or _text(_child(node, "date"))),
        "summary": _clean(summary)[:_FEED_SUMMARY_CHARS],
    }


def _atom_item(node: ElementTree.Element, feed_url: str) -> dict[str, str]:
    link = ""
    for child in node:
        if _local_name(child.tag) == "link":
            rel = child.attrib.get("rel", "alternate")
            href = child.attrib.get("href", "")
            if href and rel in ("", "alternate"):
                link = href
                break
    summary = ""
    for name in ("summary", "content"):
        summary = _text(_child(node, name))
        if summary:
            break
    return {
        "title": _clean(_text(_child(node, "title"))),
        "url": urljoin(feed_url, link),
        "published": _clean(_text(_child(node, "published")) or _text(_child(node, "updated"))),
        "summary": _clean(summary)[:_FEED_SUMMARY_CHARS],
    }


def _parse_feed(body: bytes, url: str) -> dict[str, Any]:
    if b"<!ENTITY" in body.upper():
        raise ValueError("DTD entities are not allowed in feeds")
    try:
        root = ElementTree.fromstring(body)
    except ElementTree.ParseError as exc:
        raise ValueError(f"feed is not valid XML: {exc}") from exc

    kind = _local_name(root.tag)
    if kind == "rss":
        channel = _child(root, "channel")
        if channel is None:
            raise ValueError("RSS feed has no channel")
        title = _text(_child(channel, "title"))
        items = [_rss_item(node, url) for node in channel if _local_name(node.tag) == "item"]
    elif kind == "feed":
        title = _text(_child(root, "title"))
        items = [_atom_item(node, url) for node in root if _local_name(node.tag) == "entry"]
    else:
        raise ValueError("document is not an RSS or Atom feed")

    return {
        "url": url,
        "title": _clean(title) or url,
        "items": [item for item in items if item["title"]][:_FEED_MAX_ITEMS],
    }


class NewsFetchTool:
    """Fetch RSS/Atom feeds over the network and return normalised news items."""

    name = "tool_news_fetch"
    version = "1"
    description = (
        "Fetch one or more RSS or Atom news feeds and return their recent items. "
        "Read-only: it never writes or changes remote state."
    )
    parameters: ClassVar[dict[str, Any]] = {
        "type": "object",
        "properties": {
            "urls": {
                "type": "array",
                "items": {"type": "string"},
                "minItems": 1,
                "maxItems": 6,
                "description": "Absolute http(s) RSS or Atom feed URLs.",
            }
        },
        "required": ["urls"],
    }
    network_required = True

    def __init__(
        self,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        validate_url: Callable[[str], None] | None = None,
        timeout_seconds: float = 20.0,
    ) -> None:
        self._transport = transport
        self._validate_url = validate_url or _validate_public_url
        self._timeout = timeout_seconds

    async def run(self, arguments: dict[str, Any], *, idempotency_key: str = "") -> dict[str, Any]:
        del idempotency_key
        raw_urls = arguments.get("urls") or arguments.get("url")
        if isinstance(raw_urls, str):
            raw_urls = [raw_urls]
        if not isinstance(raw_urls, list) or not raw_urls:
            raise ValueError("urls must be a non-empty list of feed URLs")
        urls = [str(url) for url in raw_urls[:6]]

        async with httpx.AsyncClient(
            timeout=self._timeout,
            transport=self._transport,
            follow_redirects=False,
            headers={"user-agent": "agent-platform-news/1.0"},
        ) as client:
            results = await asyncio.gather(*(self._fetch(client, url) for url in urls), return_exceptions=True)

        feeds: list[dict[str, Any]] = []
        errors: list[dict[str, str]] = []
        for url, result in zip(urls, results, strict=True):
            if isinstance(result, BaseException):
                errors.append({"url": url, "error": str(result)[:200]})
            else:
                feeds.append(result)
        if not feeds:
            raise RuntimeError("all news feeds failed: " + "; ".join(error["error"] for error in errors))
        return {"fetched_at": datetime.now(UTC).isoformat(), "feeds": feeds, "errors": errors}

    async def _fetch(self, client: httpx.AsyncClient, url: str) -> dict[str, Any]:
        for _ in range(_FEED_MAX_REDIRECTS + 1):
            await asyncio.to_thread(self._validate_url, url)
            async with client.stream("GET", url) as response:
                if response.status_code in {301, 302, 303, 307, 308}:
                    location = response.headers.get("location", "")
                    if not location:
                        raise ValueError(f"redirect from {url} has no location")
                    url = urljoin(url, location)
                    continue
                response.raise_for_status()
                body = bytearray()
                async for chunk in response.aiter_bytes():
                    body.extend(chunk)
                    if len(body) > _FEED_MAX_BYTES:
                        raise ValueError("feed exceeds 2 MiB")
                return _parse_feed(bytes(body), url)
        raise ValueError("too many redirects")
