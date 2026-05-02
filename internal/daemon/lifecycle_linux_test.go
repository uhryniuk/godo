package daemon

import (
	"os"
	"strings"
	"testing"
)

func procIsStopped(t *testing.T, pid int) bool {
	t.Helper()
	body, err := os.ReadFile("/proc/" + itoa(pid) + "/status")
	if err != nil {
		t.Fatalf("read /proc/%d/status: %v", pid, err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.Contains(line, "T (stopped)") || strings.Contains(line, "t (tracing")
		}
	}
	return false
}
