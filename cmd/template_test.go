package cmd

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/uhryniuk/godo/internal/service"
)

func TestRenderTemplate_ProducesValidSpec(t *testing.T) {
	body := renderTemplate("alpha")

	var s service.Spec
	if _, err := toml.Decode(string(body), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// service.Validate requires a non-empty command. The template's
	// uncommented `command = "echo"` is what makes the file immediately
	// loadable — guard against future edits that comment it out.
	if s.Command != "echo" {
		t.Errorf("Command: got %q, want %q", s.Command, "echo")
	}
	if err := s.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestRenderTemplate_ContainsName(t *testing.T) {
	body := string(renderTemplate("my-service"))
	if !strings.Contains(body, "my-service") {
		t.Errorf("rendered template missing name placeholder: %q", body)
	}
}

func TestRenderTemplate_NoUnreplacedPlaceholder(t *testing.T) {
	body := string(renderTemplate("foo"))
	if strings.Contains(body, "<NAME>") {
		t.Errorf("rendered template still contains <NAME> placeholder")
	}
}
