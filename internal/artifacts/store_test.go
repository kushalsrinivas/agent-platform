package artifacts

import (
	"context"
	"errors"
	"testing"
)

func TestStoreRoundTripAndConfinement(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, "checkpoints/exec_1/ckpt_1.json.gz", []byte("state")); err != nil {
		t.Fatal(err)
	}
	data, err := store.Get(ctx, "checkpoints/exec_1/ckpt_1.json.gz")
	if err != nil || string(data) != "state" {
		t.Fatalf("get = %q, %v", data, err)
	}
	if _, err := store.Get(ctx, "checkpoints/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing object error = %v", err)
	}
	if _, err := store.path("../escape"); err == nil {
		t.Fatal("parent traversal must be rejected")
	}
}

func TestEmptyStoreDisablesOffload(t *testing.T) {
	store, err := New("")
	if err != nil || store != nil {
		t.Fatalf("empty store = %v, %v", store, err)
	}
}
