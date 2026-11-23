# End-to-end tests (deferred)

Browser-driven flows exercised through the dashboard against a real API: start an execution, watch SSE updates,
cancel it, inspect a failed attempt.

Playwright is the intended tool. The suite lands with the first complete vertical slice (Phase 03/04) so the
selectors are written against real behaviour, not fixtures. Unit tests are colocated with Go and Python code;
infrastructure integration tests live in [`../integration`](../integration/README.md).
