package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pavp/tkt/internal/paths"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/pavp/tkt/internal/workspace"
)

var kindValues = regexp.MustCompile(`(?s)--kind\b.*?\[possible values: ([^\]]*)\]`)

// desktopKinds are the agents tkt offers on Herdr desktop, named after their executables. Herdr desktop keeps its
// agent list inside the app, where only mods reach it (knownAgents), and hc lists only the user's own templates,
// so tkt offers the kinds Herdr 0.9.5 lists.
var desktopKinds = []string{"pi", "claude", "codex", "gemini", "cursor", "devin", "agy", "cline", "omp", "mastracode", "opencode",
	"copilot", "kimi", "kiro", "droid", "amp", "grok", "hermes", "kilo", "qodercli", "qwen", "letta", "maki", "muse"}

// agentKinds are the agents Herdr can start whose executable is in PATH, in Herdr's order. Herdr names each kind
// after its executable and lists the kinds only in its help, so tkt keeps no list of its own for it and never falls
// back to one agent.
func (a *App) agentKinds(ctx context.Context) ([]string, error) {
	if ch, err := a.whichHerdr(ctx); err == nil && ch.flavor == herdrDesktop {
		if known := a.loadAgentKinds(); len(known) > 0 {
			return a.installedKinds(known, herdrDesktop)
		}
		return a.installedKinds(desktopKinds, herdrDesktop)
	}
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
	return a.installedKinds(all, herdrClassic)
}

func (a *App) installedKinds(all []string, flavor herdrFlavor) ([]string, error) {
	var kinds []string
	for _, k := range all {
		if _, err := a.Run.LookPath(k); err == nil {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no agent %s can start is installed; install one of: %s", flavor, strings.Join(all, ", "))
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
		if _, ok := unattendedOf(ctx); ok {
			kinds, err := a.agentKinds(ctx)
			if err != nil {
				return "", err
			}
			if !slices.Contains(kinds, asked) {
				return "", fmt.Errorf("%q is not an agent kind: use one of %s", asked, strings.Join(kinds, ", "))
			}
		}
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
	if err := needAnswer(ctx, workspace.NeedAgent); err != nil {
		return "", err
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

// agentChoices are the agents a Start can open, with the one last chosen, which a board offers first.
func (a *App) agentChoices(ctx context.Context) (kinds []string, last string, err error) {
	kinds, err = a.agentKinds(ctx)
	return kinds, a.loadState().LastAgent, err
}

func (a *App) agentKindsFile() string { return filepath.Join(a.Env.DataDir(), "herdr-agents.json") }

// saveAgentKinds keeps the agents Herdr desktop knows, as its tkt mod reads them, most picked first.
func (a *App) saveAgentKinds(kinds []string) error {
	b, err := json.Marshal(struct {
		Agents []string `json:"agents"`
	}{kinds})
	if err != nil {
		return err
	}
	return paths.WritePrivateFile(a.agentKindsFile(), b)
}

func (a *App) loadAgentKinds() []string {
	var saved struct {
		Agents []string `json:"agents"`
	}
	if b, err := os.ReadFile(a.agentKindsFile()); err != nil || json.Unmarshal(b, &saved) != nil {
		return nil
	}
	return saved.Agents
}
