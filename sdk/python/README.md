# Python SDK

Typed, async client for the public REST API. It is generated from nothing and adds nothing: two `GET`s today,
more resource clients as the API grows.

```python
from agent_platform import Client

async with Client("http://localhost:8080", token="...") as api:
    print(await api.meta())
```
