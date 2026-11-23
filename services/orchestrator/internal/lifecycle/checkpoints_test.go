package lifecycle

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func gzipJSON(t *testing.T, payload string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestCheckpointPayloadMustBeGzipJSON(t *testing.T) {
	if err := validateCheckpointPayload(gzipJSON(t, `{"schema_version":1,"state":{"steps":1}}`)); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	if err := validateCheckpointPayload([]byte("pickle")); err == nil {
		t.Fatal("uncompressed payload must be rejected")
	}
	if err := validateCheckpointPayload(gzipJSON(t, `not json`)); err == nil {
		t.Fatal("non-JSON checkpoint must be rejected")
	}
}
