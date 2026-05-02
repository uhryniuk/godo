package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderServicesTable_Empty(t *testing.T) {
	var buf strings.Builder
	renderServicesTable(&buf, nil)
	if !strings.Contains(buf.String(), "(no services)") {
		t.Errorf("expected (no services), got %q", buf.String())
	}
}

func TestRenderServicesTable_MixedRows(t *testing.T) {
	rows := []serviceRow{
		{Name: "web", Command: "node server.js", Autostart: "yes", Restart: "on-failure", Cron: "-", File: "web.toml", Status: "ok"},
		{Name: "", Command: "", Autostart: "", Restart: "", Cron: "", File: "broken.toml", Status: "error: command is required"},
	}
	var buf strings.Builder
	renderServicesTable(&buf, rows)
	out := buf.String()

	for _, want := range []string{"NAME", "COMMAND", "AUTOSTART", "STATUS", "web", "node server.js", "broken.toml", "error:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestServicesIntegration_FilesOnly(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeFile("alpha.toml", "name=\"alpha\"\ncommand=\"/bin/true\"\n")
	writeFile("beta.toml", "name=\"beta\"\ncommand=\"/bin/echo\"\nautostart=true\nrestart=\"on-failure\"\n")
	writeFile("broken.toml", "name=\"broken\"\n") // missing command

	rows := gatherServiceRows(dir)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// Rows should be sorted alphabetically by name/file.
	if rows[0].Name != "alpha" {
		t.Errorf("row[0] name: got %q, want alpha", rows[0].Name)
	}
	if rows[0].Status != "ok" {
		t.Errorf("row[0] status: got %q, want ok", rows[0].Status)
	}
	if rows[1].Name != "beta" {
		t.Errorf("row[1] name: got %q, want beta", rows[1].Name)
	}
	if rows[1].Autostart != "yes" {
		t.Errorf("row[1] autostart: got %q, want yes", rows[1].Autostart)
	}
	if rows[1].Restart != "on-failure" {
		t.Errorf("row[1] restart: got %q, want on-failure", rows[1].Restart)
	}

	// broken.toml sorts as "broken.toml" (no Name) — should be last here
	// since "broken.toml" > "beta" alphabetically.
	errorRow := rows[2]
	if !strings.HasPrefix(errorRow.Status, "error:") {
		t.Errorf("error row status: got %q, want error:...", errorRow.Status)
	}
	if errorRow.File != "broken.toml" {
		t.Errorf("error row file: got %q, want broken.toml", errorRow.File)
	}
}
