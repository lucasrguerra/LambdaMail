package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every embedded Postgres in this package binds a fixed port, and those ports
// must sit below the kernel's ephemeral range.
//
// They did not. All sixteen were inside 32768-60999, which is the range Linux
// draws source ports from for outgoing connections. Nothing in this repository
// was listening on them - the kernel simply handed one out to some unrelated
// connection at the moment a test tried to bind it, and that test died with
// "process already listening on port 54340". It is a race, so it failed rarely
// and never in the same place twice, which is exactly what makes it expensive.
//
// This test reads the ports out of the source rather than from a list someone
// has to remember to update: the next person adding an e2e test gets told here
// instead of by a red build a week later.

// ephemeralFloor is the lowest port Linux will hand out as a source port
// (net.ipv4.ip_local_port_range). Staying below it means the kernel will never
// allocate one of these behind our back.
const ephemeralFloor = 32768

var portPattern = regexp.MustCompile(`Port\((\d+)\)`)

func TestEmbeddedPostgresPortsAreOutsideTheEphemeralRange(t *testing.T) {
	entries, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	found := 0
	for _, name := range entries {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, match := range portPattern.FindAllStringSubmatch(string(source), -1) {
			port, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}
			found++
			if port >= ephemeralFloor {
				t.Errorf("%s binds port %d, which is inside the kernel's ephemeral "+
					"range (>= %d): the kernel can hand it to an unrelated connection "+
					"and this test will fail intermittently with "+
					"\"process already listening\". Pick a port below %d.",
					name, port, ephemeralFloor, ephemeralFloor)
			}
		}
	}

	if found == 0 {
		t.Fatal("no embedded Postgres ports found; this test is no longer checking anything")
	}
}

// Two tests sharing a port would collide every time rather than intermittently.
func TestEmbeddedPostgresPortsAreUnique(t *testing.T) {
	entries, _ := filepath.Glob("*_test.go")

	seen := map[int]string{}
	for _, name := range entries {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, match := range portPattern.FindAllStringSubmatch(string(source), -1) {
			port, _ := strconv.Atoi(match[1])
			if previous, taken := seen[port]; taken {
				t.Errorf("port %d is bound by both %s and %s", port, previous, name)
			}
			seen[port] = name
		}
	}
}

// The port in the connection string has to be the one the server was started
// on, or the test connects to whatever else happens to be there.
func TestEachTestConnectsToThePortItStarted(t *testing.T) {
	entries, _ := filepath.Glob("*_test.go")

	for _, name := range entries {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(source)
		matches := portPattern.FindAllStringSubmatch(text, -1)
		if len(matches) != 1 {
			continue
		}
		port := matches[0][1]
		if strings.Contains(text, "postgres://") && !strings.Contains(text, "localhost:"+port) {
			t.Errorf("%s starts Postgres on port %s but its connection string points "+
				"somewhere else", name, port)
		}
	}
}
