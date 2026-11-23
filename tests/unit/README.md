# Unit tests

Unit tests are colocated with their code, by language and by package:

- Go: `*_test.go` next to the package (`internal/platform/...`, `sdk/go/...`). Run `make test-go`.
- Python: `runtimes/python/tests` and `sdk/python/tests`. Run `make test-python`.

There is deliberately nothing in this directory. Moving tests away from the package they test makes coverage and
ownership worse; the brief's directory exists so the decision is explicit.
