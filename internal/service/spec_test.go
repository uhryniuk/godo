package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSpecFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "web.toml", `
name = "web"
command = "node"
args = ["server.js"]
working_dir = "/srv/web"
autostart = true
restart = "on-failure"
nice = 5

[env]
PORT = "8080"

[cron]
schedule = "*/5 * * * *"
`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "web" {
		t.Errorf("Name: got %q", s.Name)
	}
	if s.Command != "node" {
		t.Errorf("Command: got %q", s.Command)
	}
	if len(s.Args) != 1 || s.Args[0] != "server.js" {
		t.Errorf("Args: got %v", s.Args)
	}
	if s.WorkingDir != "/srv/web" {
		t.Errorf("WorkingDir: got %q", s.WorkingDir)
	}
	if !s.Autostart {
		t.Errorf("Autostart: should be true")
	}
	if s.Restart != "on-failure" {
		t.Errorf("Restart: got %q", s.Restart)
	}
	if s.Nice != 5 {
		t.Errorf("Nice: got %d", s.Nice)
	}
	if s.Env["PORT"] != "8080" {
		t.Errorf("Env[PORT]: got %q", s.Env["PORT"])
	}
	if s.Cron.Schedule != "*/5 * * * *" {
		t.Errorf("Cron.Schedule: got %q", s.Cron.Schedule)
	}
	if s.Path != path {
		t.Errorf("Path: got %q, want %q", s.Path, path)
	}
	if s.Hash == "" {
		t.Errorf("Hash should be populated")
	}
}

func TestLoadNameDefaultsToFilename(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "my-worker.toml", `
command = "/bin/echo"
`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "my-worker" {
		t.Errorf("Name: got %q, want %q", s.Name, "my-worker")
	}
}

func TestLoadMissingCommandIsError(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "broken.toml", `
name = "broken"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("error message: got %q", err.Error())
	}
}

func TestLoadInvalidRestartIsError(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "bad.toml", `
name = "bad"
command = "/bin/true"
restart = "sometimes"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid restart policy")
	}
	if !strings.Contains(err.Error(), "restart") {
		t.Errorf("error message: got %q", err.Error())
	}
}

func TestLoadValidRestartPolicies(t *testing.T) {
	for _, policy := range []string{"no", "on-failure", "always"} {
		t.Run(policy, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSpecFile(t, dir, "ok.toml",
				"name = \"ok\"\ncommand = \"/bin/true\"\nrestart = \""+policy+"\"\n")
			if _, err := Load(path); err != nil {
				t.Errorf("Load failed for restart=%s: %v", policy, err)
			}
		})
	}
}

func TestLoadInvalidCronIsError(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "cron-bad.toml", `
name = "cron"
command = "/bin/true"

[cron]
schedule = "not a real cron expr"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid cron schedule")
	}
	if !strings.Contains(err.Error(), "cron schedule") {
		t.Errorf("error: %q", err.Error())
	}
}

func TestLoadValidCronVariants(t *testing.T) {
	for _, sched := range []string{"* * * * *", "0 4 * * *", "*/15 * * * *"} {
		t.Run(sched, func(t *testing.T) {
			dir := t.TempDir()
			body := "name = \"x\"\ncommand = \"/bin/true\"\n[cron]\nschedule = \"" + sched + "\"\n"
			path := writeSpecFile(t, dir, "x.toml", body)
			if _, err := Load(path); err != nil {
				t.Errorf("Load failed for cron=%q: %v", sched, err)
			}
		})
	}
}

func TestLoadAllReturnsSpecsInPathOrder(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "b.toml", "name=\"b\"\ncommand=\"/bin/true\"\n")
	writeSpecFile(t, dir, "a.toml", "name=\"a\"\ncommand=\"/bin/true\"\n")
	writeSpecFile(t, dir, "c.toml", "name=\"c\"\ncommand=\"/bin/true\"\n")

	specs, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("count: got %d", len(specs))
	}
	want := []string{"a", "b", "c"}
	for i, s := range specs {
		if s.Name != want[i] {
			t.Errorf("position %d: got %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestLoadAllSkipsBadFilesButReportsError(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "ok.toml", "name=\"ok\"\ncommand=\"/bin/true\"\n")
	writeSpecFile(t, dir, "bad.toml", "name=\"bad\"\n") // missing command

	specs, err := LoadAll(dir)
	if err == nil {
		t.Fatal("expected aggregated error from bad.toml")
	}
	if len(specs) != 1 || specs[0].Name != "ok" {
		t.Errorf("expected 1 good spec, got %d (%v)", len(specs), specs)
	}
	if !strings.Contains(err.Error(), "bad.toml") {
		t.Errorf("error should reference bad.toml; got %q", err.Error())
	}
}

func TestLoadEmptyDirIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	specs, err := LoadAll(dir)
	if err != nil {
		t.Errorf("LoadAll: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("expected empty, got %d", len(specs))
	}
}

func TestSpecHashChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "hashy.toml", "name=\"x\"\ncommand=\"/bin/true\"\n")
	s1, err := Load(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if err := os.WriteFile(path, []byte("name=\"x\"\ncommand=\"/bin/false\"\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	s2, err := Load(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if s1.Hash == s2.Hash {
		t.Errorf("hash should change with content; got identical %q", s1.Hash)
	}
}

func TestJobOptionsCarriesAllFields(t *testing.T) {
	dir := t.TempDir()
	path := writeSpecFile(t, dir, "full.toml", `
name = "full"
command = "node"
working_dir = "/x"
nice = 3
ionice = "best-effort:4"
restart = "always"

[env]
A = "1"

[cron]
schedule = "*/5 * * * *"
`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	opts := s.JobOptions()
	// Smoke-test: applying the opts to a fresh job should populate
	// every field we care about. (We don't test individual options here
	// because job_test.go already does.)
	if len(opts) < 7 {
		t.Errorf("expected >=7 options for fully-populated spec, got %d", len(opts))
	}
}
