package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pavp/tkt/internal/execx"
)

// Token reads the GitHub token of a gh account without switching gh's active account.
// An empty account means gh's active account.
func Token(ctx context.Context, run execx.Runner, account string) (string, error) {
	args := []string{"auth", "token"}
	if account != "" {
		args = append(args, "--user", account)
	}
	out, err := run.Run(ctx, "", "gh", args, nil)
	if err != nil {
		who := "the active gh account"
		if account != "" {
			who = "gh account " + account
		}
		return "", fmt.Errorf("no GitHub token for %s (run `gh auth login`): %w", who, err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", errors.New("gh returned an empty token")
	}
	return tok, nil
}
