package execution

import "testing"

func TestCheckpointSequenceParsing(t *testing.T) {
	sequence, err := checkpointSequence("ckpt-000042-abcdef.json.gz")
	if err != nil || sequence != 42 {
		t.Fatalf("sequence = %d, %v", sequence, err)
	}
	for _, name := range []string{"checkpoint.json.gz", "ckpt-x-abc.json.gz", "ckpt-000000-abc.json.gz"} {
		if _, err := checkpointSequence(name); err == nil {
			t.Fatalf("%q must be rejected", name)
		}
	}
}
