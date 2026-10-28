package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var kindValues = regexp.MustCompile(`(?s)--kind\b.*?\[possible values: ([^\]]*)\]`)

// agentKinds are the agents Herdr can start whose executable is in PATH, in Herdr's order. Herdr names each kind
// after its executable and lists the kinds only in its help, so tkt keeps no list of its own and never falls back
// to one agent.
func (a *App) agentKinds(ctx context.Context) ([]string, error) {
	if _, err := a.Run.LookPath("herdr"); err != nil {
		return nil, errors.New("cannot tell which agents are installed: tkt learns them from Herdr (`herdr agent start --help`), which is not in PATH")
	}
	help, _ := a.Run.Run(ctx, "", "herdr", []string{"agent", "start", "--help"}, nil)
	var all []string
	if m := kindValues.FindSubmatch(help); m != nil {
		for k := range strings.SplitSeq(string(m[1]), ",") {
			if k = strings.TrimSpace(k); k != "" {
				all = append(all, k)
			}
		}
	}
	if len(all) == 0 {
		return nil, errors.New("cannot tell which agents Herdr can start: `herdr agent start --help` lists none; update Herdr")
	}
	var kinds []string
	for _, k := range all {
		if _, err := a.Run.LookPath(k); err == nil {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no agent Herdr can start is installed; install one of: %s", strings.Join(all, ", "))
	}
	return kinds, nil
}

// pickAgent is the agent a Start opens: the one asked for, the only one installed, or the one the user picks, the
// last one chosen offered first. The choice is remembered.
func (a *App) pickAgent(ctx context.Context, asked string) (string, error) {
	kind, err := a.chooseAgent(ctx, asked)
	if err != nil {
		return "", err
	}
	st := a.loadState()
	st.LastAgent = kind
	a.saveState(st)
	return kind, nil
}

func (a *App) chooseAgent(ctx context.Context, asked string) (string, error) {
	if asked != "" {
		if _, err := a.Run.LookPath(asked); err != nil {
			return "", fmt.Errorf("agent %s is not installed (not found in PATH)", asked)
		}
		return asked, nil
	}
	kinds, err := a.agentKinds(ctx)
	if err != nil {
		return "", err
	}
	if len(kinds) == 1 {
		return kinds[0], nil
	}
	last := a.loadState().LastAgent
	def := max(slices.Index(kinds, last), 0)
	for i, k := range kinds {
		mark := ""
		if k == last {
			mark = "  (last used)"
		}
		fmt.Fprintf(a.Stderr, "  %d) %s%s\n", i+1, k, mark)
	}
	ans, err := a.readLine(fmt.Sprintf("Open which agent? [%d] ", def+1))
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if ans == "" {
		return kinds[def], nil
	}
	n, err := strconv.Atoi(ans)
	if err != nil || n < 1 || n > len(kinds) {
		return "", fmt.Errorf("%q is not one of 1-%d", ans, len(kinds))
	}
	return kinds[n-1], nil
}

// defaultAgent is the agent the board opens without asking: the last one chosen while it is installed, else the
// first Herdr lists.
func (a *App) defaultAgent(ctx context.Context) (string, error) {
	kinds, err := a.agentKinds(ctx)
	if err != nil {
		return "", err
	}
	if last := a.loadState().LastAgent; slices.Contains(kinds, last) {
		return last, nil
	}
	return kinds[0], nil
}
