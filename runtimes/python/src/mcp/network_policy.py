"""Outbound URL policy for MCP HTTP servers and any future tenant-configured HTTP tool.

The default is deny-private. Deployments that intentionally talk to an on-premises MCP server set
``AGENT_MCP_ALLOW_PRIVATE_NETWORKS=true``; a production deployment should instead front private servers with a
controlled egress proxy.
"""

from __future__ import annotations

import ipaddress
import os
import socket
from urllib.parse import urlsplit

BLOCKED_HOSTS = {"metadata.google.internal", "metadata", "instance-data"}


class URLPolicyError(ValueError):
    pass


def _allow_private() -> bool:
    return os.getenv("AGENT_MCP_ALLOW_PRIVATE_NETWORKS", "false").strip().lower() in {"1", "true", "yes"}


def _forbidden(address: str) -> bool:
    candidate = ipaddress.ip_address(address)
    return (
        candidate.is_private
        or candidate.is_loopback
        or candidate.is_link_local
        or candidate.is_multicast
        or candidate.is_reserved
        or candidate.is_unspecified
    )


def validate_outbound_url(url: str) -> None:
    """Raise URLPolicyError unless the URL is an http(s) endpoint outside blocked address space."""
    parts = urlsplit(url)
    if parts.scheme not in {"http", "https"}:
        raise URLPolicyError("MCP URL must use http or https")
    if not parts.hostname:
        raise URLPolicyError("MCP URL must include a host")
    if parts.username or parts.password:
        raise URLPolicyError("MCP URL must not embed credentials")
    host = parts.hostname.rstrip(".").lower()
    if host in BLOCKED_HOSTS:
        raise URLPolicyError("MCP URL points at a metadata service")
    if _allow_private():
        return
    try:
        infos = socket.getaddrinfo(host, None, type=socket.SOCK_STREAM)
    except OSError as exc:
        raise URLPolicyError("MCP URL host could not be resolved") from exc
    addresses = {str(info[4][0]) for info in infos}
    if not addresses:
        raise URLPolicyError("MCP URL host could not be resolved")
    for address in addresses:
        if _forbidden(address):
            raise URLPolicyError("MCP URL resolves to private or link-local address space")
