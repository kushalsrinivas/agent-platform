from __future__ import annotations

import httpx
import pytest

from tool_runtime import NewsFetchTool
from tool_runtime.builtin import _validate_public_url

RSS = b"""<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>BBC News - UK</title>
<item>
  <title>First &amp; story</title>
  <link>https://example.com/one</link>
  <pubDate>Tue, 24 Nov 2026 10:00:00 GMT</pubDate>
  <description>&lt;p&gt;A short summary.&lt;/p&gt;</description>
</item>
<item><title>Second story</title><link>/two</link><description>Another summary</description></item>
</channel></rss>"""

ATOM = b"""<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>The Guardian</title>
<entry>
  <title>Atom story</title>
  <link rel="alternate" href="https://example.com/atom-story"/>
  <published>2026-11-24T09:00:00Z</published>
  <summary>Atom summary</summary>
</entry>
</feed>"""


def _tool(handler: httpx.MockTransport) -> NewsFetchTool:
    return NewsFetchTool(transport=handler, validate_url=lambda url: None)


async def test_news_fetch_parses_rss() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["user-agent"] == "agent-platform-news/1.0"
        return httpx.Response(200, content=RSS)

    result = await _tool(httpx.MockTransport(handler)).run({"urls": ["https://feeds.example.com/uk.xml"]})

    assert result["fetched_at"]
    assert result["errors"] == []
    feed = result["feeds"][0]
    assert feed["title"] == "BBC News - UK"
    assert feed["items"][0] == {
        "title": "First & story",
        "url": "https://example.com/one",
        "published": "Tue, 24 Nov 2026 10:00:00 GMT",
        "summary": "A short summary.",
    }
    assert feed["items"][1]["url"] == "https://feeds.example.com/two"


async def test_news_fetch_parses_atom() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, content=ATOM)

    result = await _tool(httpx.MockTransport(handler)).run({"urls": ["https://example.com/atom.xml"]})

    feed = result["feeds"][0]
    assert feed["title"] == "The Guardian"
    assert feed["items"][0] == {
        "title": "Atom story",
        "url": "https://example.com/atom-story",
        "published": "2026-11-24T09:00:00Z",
        "summary": "Atom summary",
    }


async def test_news_fetch_keeps_partial_results() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/down":
            return httpx.Response(503)
        return httpx.Response(200, content=RSS)

    result = await _tool(httpx.MockTransport(handler)).run(
        {"urls": ["https://feeds.example.com/up", "https://feeds.example.com/down"]}
    )

    assert len(result["feeds"]) == 1
    assert result["errors"][0]["url"].endswith("/down")


async def test_news_fetch_fails_when_every_feed_fails() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(503)

    with pytest.raises(RuntimeError):
        await _tool(httpx.MockTransport(handler)).run({"urls": ["https://feeds.example.com/down"]})


async def test_news_fetch_revalidates_redirect_targets() -> None:
    def validate(url: str) -> None:
        if "127.0.0.1" in url:
            raise ValueError("URL resolves to a non-public address")

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(302, headers={"location": "http://127.0.0.1/secret"})

    tool = NewsFetchTool(transport=httpx.MockTransport(handler), validate_url=validate)
    with pytest.raises(RuntimeError, match="non-public"):
        await tool.run({"urls": ["https://feeds.example.com/redirect"]})


def test_news_fetch_rejects_private_and_non_http_urls() -> None:
    for url in ("http://127.0.0.1/feed", "http://10.0.0.8/feed", "file:///etc/passwd", "http://[::1]/feed"):
        with pytest.raises(ValueError):
            _validate_public_url(url)
