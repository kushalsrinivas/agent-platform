# Test fixtures

Cross-language fixtures — workflow graphs, execution inputs and expected outcomes — shared by Go integration
tests and Python runtime tests.

The real workflow graph shape is defined in
[ADR 0007](../../docs/adr/0007-workflow-graph-representation.md); fixtures are added with the Phase 03 validator so
that both languages are tested against the same bytes. Unit-test-local fixtures stay beside their tests.
