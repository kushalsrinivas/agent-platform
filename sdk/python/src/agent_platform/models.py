"""Response models."""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict


class APIModel(BaseModel):
    model_config = ConfigDict(extra="allow", frozen=True)


class Health(APIModel):
    status: str


class Meta(APIModel):
    name: str
    environment: str
    version: str
    commit: str
    build_date: str
    time: str
