# Compose overrides

The canonical local stack is the root [`docker-compose.yml`](../../docker-compose.yml). Add an override here when
a second environment genuinely differs (for example, a CI override that removes host port mappings). Do not fork
the whole file; Compose merges overrides.

CI starts individual services from the root file and does not use an override today.
