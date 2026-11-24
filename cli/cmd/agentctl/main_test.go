package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("AGENTCTL_CONFIG", path)
	cfg := defaultConfig()
	cfg.APIKey = "apk_test"
	cfg.OrgID = "org_1"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKey != "apk_test" || loaded.OrgID != "org_1" || loaded.APIURL != cfg.APIURL {
		t.Fatalf("loaded config = %+v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestReadJSONValue(t *testing.T) {
	value, err := readJSONValue(`{"topic":"hello"}`)
	if err != nil || value["topic"] != "hello" {
		t.Fatalf("value=%v err=%v", value, err)
	}
}
