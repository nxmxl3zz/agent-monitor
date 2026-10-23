package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigProfileIsolatesSettings(t *testing.T) {
	t.Setenv("OPENUSAGE_CONFIG_DIR", "")
	defaultPath := ConfigPath()
	profileDir := filepath.Join(t.TempDir(), "monitor")
	t.Setenv("OPENUSAGE_CONFIG_DIR", "  "+profileDir+"  ")

	cfg := DefaultConfig()
	cfg.AutoDetect = false
	cfg.Theme = "Nord"
	if err := Save(cfg); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if ConfigPath() == defaultPath {
		t.Fatal("profile resolved to the default settings path")
	}
	if _, err := os.Stat(filepath.Join(profileDir, "settings.json")); err != nil {
		t.Fatalf("profile settings missing: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if got.AutoDetect || got.Theme != "Nord" {
		t.Fatalf("profile not loaded: auto_detect=%t theme=%q", got.AutoDetect, got.Theme)
	}

	t.Setenv("OPENUSAGE_CONFIG_DIR", " \t")
	if ConfigPath() != defaultPath {
		t.Fatal("empty override did not restore the default settings path")
	}
}
