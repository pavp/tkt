package execx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs external programs. Production code uses OS; tests inject fakes.
type Runner interface {
	Run(ctx context.Context, dir, name string, args []string, stdin []byte) ([]byte, error)
	LookPath(name string) (string, error)
}

type OS struct{}

func (OS) Run(ctx context.Context, dir, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w", name, err)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return stdout.Bytes(), nil
}

func (OS) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}
