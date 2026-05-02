package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEditor_EnvWins(t *testing.T) {
	t.Setenv("EDITOR", "from-env")
	cfg := &CliConfig{Editor: "from-config"}
	if got := cfg.ResolveEditor(); got != "from-env" {
		t.Errorf("ResolveEditor: got %q, want %q", got, "from-env")
	}
}

func TestResolveEditor_ConfigWhenEnvUnset(t *testing.T) {
	t.Setenv("EDITOR", "")
	cfg := &CliConfig{Editor: "from-config"}
	if got := cfg.ResolveEditor(); got != "from-config" {
		t.Errorf("ResolveEditor: got %q, want %q", got, "from-config")
	}
}

func TestResolveEditor_DefaultVim(t *testing.T) {
	t.Setenv("EDITOR", "")
	cfg := &CliConfig{}
	if got := cfg.ResolveEditor(); got != "vim" {
		t.Errorf("ResolveEditor: got %q, want %q", got, "vim")
	}
}

func TestResolveEditor_NilReceiverIsSafe(t *testing.T) {
	// Defensive: callers that wire an unparsed config shouldn't crash.
	t.Setenv("EDITOR", "")
	var cfg *CliConfig
	if got := cfg.ResolveEditor(); got != "vim" {
		t.Errorf("ResolveEditor on nil: got %q, want %q", got, "vim")
	}
}

func TestInitConfig_ParsesEditorFromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-write a config.toml with an editor key. InitConfig will touch
	// the file but not overwrite it.
	cfgPath := filepath.Join(home, CONFIG_DIR, CONFIG_FILE)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(`editor = "nano"`+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := InitConfig()
	if cfg.Editor != "nano" {
		t.Errorf("Editor: got %q, want %q", cfg.Editor, "nano")
	}
}

func TestInitConfig_EmptyFileIsValid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := InitConfig()
	if cfg == nil {
		t.Fatal("InitConfig returned nil")
	}
	if cfg.Editor != "" {
		t.Errorf("Editor on empty config: got %q, want empty", cfg.Editor)
	}
}
