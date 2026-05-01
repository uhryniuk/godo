package cmd

import (
	"reflect"
	"testing"
)

func TestConsumeLeadingName(t *testing.T) {
	cases := []struct {
		desc     string
		in       []string
		wantName string
		wantArgs []string
		wantErr  bool
	}{
		{"no args", nil, "", nil, false},
		{"plain command — no consumption", []string{"sh", "-c", "x"}, "", []string{"sh", "-c", "x"}, false},
		{"--name space form", []string{"--name", "web", "sh", "-c", "x"}, "web", []string{"sh", "-c", "x"}, false},
		{"-n space form", []string{"-n", "web", "sh", "-c", "x"}, "web", []string{"sh", "-c", "x"}, false},
		{"--name= form", []string{"--name=web", "sh", "-c", "x"}, "web", []string{"sh", "-c", "x"}, false},
		{"-n= form", []string{"-n=web", "sh", "-c", "x"}, "web", []string{"sh", "-c", "x"}, false},
		{"--name without value", []string{"--name"}, "", nil, true},
		{"-n without value", []string{"-n"}, "", nil, true},
		{"--name= empty", []string{"--name=", "sh"}, "", nil, true},
		// Critical: do not eat --name when it appears AFTER the command,
		// since DisableFlagParsing means everything after the command
		// belongs to the child program.
		{"--name in child args is preserved", []string{"sh", "-c", "echo --name test"}, "", []string{"sh", "-c", "echo --name test"}, false},
		{"--name as child flag is preserved", []string{"my-tool", "--name", "passthrough"}, "", []string{"my-tool", "--name", "passthrough"}, false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			gotName, gotArgs, err := consumeLeadingName(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err: got %v, wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if gotName != c.wantName {
				t.Errorf("name: got %q, want %q", gotName, c.wantName)
			}
			if !reflect.DeepEqual(gotArgs, c.wantArgs) {
				t.Errorf("args: got %v, want %v", gotArgs, c.wantArgs)
			}
		})
	}
}
