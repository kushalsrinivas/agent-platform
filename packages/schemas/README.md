# Shared schemas

JSON Schemas for agent version specs and workflow graphs arrive with the Phase 03 control-plane API, alongside
their validation in Go and Python. They will live here so both languages validate against one file.

Phase 02 deliberately does not invent the schema: the authoritative rules are in
[ADR 0007](../../docs/adr/0007-workflow-graph-representation.md), and a schema written before its validator would
drift.
