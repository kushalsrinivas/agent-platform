package config

import "testing"

func TestLoadReadsEmbeddedCommonAndServiceFields(t *testing.T) {
	t.Setenv("ENVIRONMENT", "test")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("API_HTTP_ADDR", ":9999")

	type serviceConfig struct {
		Common
		HTTPAddr string `env:"API_HTTP_ADDR" envDefault:":8080"`
	}

	cfg, err := Load[serviceConfig]()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "test" || cfg.LogLevel != "debug" {
		t.Fatalf("embedded config not loaded: %+v", cfg.Common)
	}
	if cfg.HTTPAddr != ":9999" {
		t.Fatalf("HTTPAddr = %q, want :9999", cfg.HTTPAddr)
	}
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	t.Setenv("API_HTTP_ADDR", ":1")

	type intConfig struct {
		Port int `env:"API_HTTP_ADDR"`
	}

	if _, err := Load[intConfig](); err == nil {
		t.Fatal("expected error for a non-integer port")
	}
}
