package executions

import "testing"

func TestValidIdempotencyKey(t *testing.T) {
	cases := map[string]bool{
		"req-12345678":           true,
		"short":                  false,
		"has space in the key":   false,
		"line\nbreak-in-the-key": false,
		"":                       false,
	}
	for key, want := range cases {
		if got := validIdempotencyKey(key); got != want {
			t.Fatalf("validIdempotencyKey(%q) = %v, want %v", key, got, want)
		}
	}
}
