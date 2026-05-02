package daemon

import (
	"os/exec"
	"strings"
	"testing"
)

// procIsStopped checks whether pid is in the stopped (T) state using ps,
// since macOS does not expose process state via /proc.
// ps -o state= returns a multi-char field like "TN" or "T+"; we check
// only the first character, which is the primary BSD process state.
func procIsStopped(t *testing.T, pid int) bool {
	t.Helper()
	out, err := exec.Command("ps", "-o", "state=", "-p", itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps -o state= -p %d: %v", pid, err)
	}
	state := strings.TrimSpace(string(out))
	return len(state) > 0 && state[0] == 'T'
}
