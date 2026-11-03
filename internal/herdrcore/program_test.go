package herdrcore_test

import (
	"errors"
	"testing"

	"github.com/pavp/tkt/internal/herdrcore"
)

func TestAgentProgramIsResolvedOnTktsPath(t *testing.T) {
	found := func(name string) (string, error) {
		if name == "claude" {
			return "/home/dev/.linuxbrew/bin/claude", nil
		}
		return "", errors.New("not found")
	}
	tests := []struct {
		name string
		look func(string) (string, error)
		kind string
		want string
	}{
		{"an agent on tkt's PATH runs by its full path, since herdr-core starts terminals with its own PATH", found, "claude", "/home/dev/.linuxbrew/bin/claude"},
		{"an agent tkt cannot find keeps its name for herdr-core to look up", found, "codex", "codex"},
		{"without a lookup the name is kept", nil, "claude", "claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := herdrcore.NewWith(&seqHC{})
			herdrcore.SetLookPath(c, tt.look)
			if got := herdrcore.Program(c, tt.kind); got != tt.want {
				t.Errorf("Program(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}
