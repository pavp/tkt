package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type ghRunner struct {
	args []string
	out  string
	err  error
}

func (g *ghRunner) Run(_ context.Context, _, name string, args []string, _ []byte) ([]byte, error) {
	g.args = append([]string{name}, args...)
	return []byte(g.out), g.err
}

func (g *ghRunner) LookPath(string) (string, error) { return "", nil }

func TestToken(t *testing.T) {
	r := &ghRunner{out: "gho_abc\n"}
	tok, err := Token(context.Background(), r, "dev-work")
	if err != nil || tok != "gho_abc" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if strings.Join(r.args, " ") != "gh auth token --user dev-work" {
		t.Errorf("args = %v", r.args)
	}
	if _, err := Token(context.Background(), r, ""); err != nil || strings.Join(r.args, " ") != "gh auth token" {
		t.Errorf("active account args = %v, %v", r.args, err)
	}
	r = &ghRunner{err: errors.New("exit status 1")}
	if _, err := Token(context.Background(), r, "dev-work"); err == nil || !strings.Contains(err.Error(), "gh account dev-work") || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("err = %v", err)
	}
	r = &ghRunner{out: "  \n"}
	if _, err := Token(context.Background(), r, ""); err == nil {
		t.Error("empty token accepted")
	}
}
