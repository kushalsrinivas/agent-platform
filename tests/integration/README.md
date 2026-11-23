# Integration tests

`infrastructure_test.go` runs against the Compose data stores after migrations:

```bash
make integration-test
```

It verifies schemas, role separation and JetStream availability. It needs no running Go service and no external
network. Unit tests live beside the code they test, not here.
