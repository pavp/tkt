package cli

import (
	"cmp"
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
	"sync"
	"time"

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
			if kinds, err := a.installedKinds(ctx, known, herdrDesktop); err == nil {
				return kinds, nil
			}
		}
		return a.installedKinds(ctx, desktopKinds, herdrDesktop)
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
	return a.installedKinds(ctx, all, herdrClassic)
}

// installedKinds are the kinds of all found in PATH, or else in the PATH of the user's interactive shell, where
// Herdr starts agents: tkt started by the mod or the daemon may run with a shorter PATH, as Homebrew on Linux adds
// itself only in .bashrc.
func (a *App) installedKinds(ctx context.Context, all []string, flavor herdrFlavor) ([]string, error) {
	var kinds []string
	var shellDirs []string
	asked := false
	for _, k := range all {
		program := k
		if run := a.agentRun(k); flavor == herdrDesktop && len(run) > 0 {
			program = run[0]
		}
		if _, err := a.Run.LookPath(program); err == nil {
			kinds = append(kinds, k)
			continue
		}
		if !asked {
			shellDirs, asked = a.shellPath(ctx), true
		}
		if executableIn(shellDirs, program) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no agent %s can start is installed; install one of: %s", flavor, strings.Join(all, ", "))
	}
	return kinds, nil
}

// startsOnDesktop reports whether the agent being chosen opens in Herdr desktop, whose programs.json may start it
// through another program.
func (a *App) startsOnDesktop(ctx context.Context) bool {
	if u, ok := unattendedOf(ctx); ok {
		return u.flavor == herdrDesktop
	}
	ch, err := a.whichHerdr(ctx)
	return err == nil && ch.flavor == herdrDesktop
}

// agentInstalled reports whether an agent's executable is in PATH, or in the PATH of the user's interactive shell.
func (a *App) agentInstalled(ctx context.Context, kind string) bool {
	_, err := a.agentPath(ctx, kind)
	return err == nil
}

// agentPath is an agent's executable in PATH, or else in the PATH of the user's interactive shell, where herdr-core,
// which starts terminals with its own PATH, cannot find it by name.
func (a *App) agentPath(ctx context.Context, kind string) (string, error) {
	if p, err := a.Run.LookPath(kind); err == nil {
		return p, nil
	}
	for _, d := range a.shellPath(ctx) {
		if p := filepath.Join(d, kind); executableIn([]string{d}, kind) {
			return p, nil
		}
	}
	return "", fmt.Errorf("agent %s is not installed (not found in PATH)", kind)
}

// shellPathCache keeps the interactive shell's PATH for a while, as asking takes a shell start on every Start.
type shellPathCache struct {
	mu   sync.Mutex
	dirs []string
	at   time.Time
}

const shellPathTTL = 10 * time.Minute

// shellPath is PATH as the user's interactive shell sets it, or nothing when that shell does not answer in time. The
// shell writes it to a file rather than to tkt's pipe, so a job its rc file leaves running cannot hold tkt waiting.
func (a *App) shellPath(ctx context.Context) []string {
	if c := a.shellCache; c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.at.IsZero() && a.Now().Sub(c.at) < shellPathTTL {
			return c.dirs
		}
	}
	dirs := a.askShellPath(ctx)
	if c := a.shellCache; c != nil {
		c.dirs, c.at = dirs, a.Now()
	}
	return dirs
}

func (a *App) askShellPath(ctx context.Context) []string {
	file := filepath.Join(a.Env.DataDir(), "shell-path-"+strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := a.Run.Run(ctx, "", "sh", []string{"-c", shellPathScript(cmp.Or(a.Env.Getenv("SHELL"), "sh"), file)}, nil); err != nil {
		return nil
	}
	b, err := os.ReadFile(file)
	_ = os.Remove(file)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, d := range filepath.SplitList(strings.TrimSpace(string(b))) {
		if filepath.IsAbs(d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// shellPathScript runs the interactive shell with its output going to file and nothing tied to tkt's pipes.
func shellPathScript(shell, file string) string {
	return fmt.Sprintf(`%s -ic 'printf %%s "$PATH"' >%s 2>/dev/null </dev/null`, shellQuote(shell), shellQuote(file))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func executableIn(dirs []string, name string) bool {
	for _, d := range dirs {
		if info, err := os.Stat(filepath.Join(d, name)); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
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
		program := asked
		if run := a.agentRun(asked); len(run) > 0 && a.startsOnDesktop(ctx) {
			program = run[0]
		}
		if !a.agentInstalled(ctx, program) {
			if program != asked {
				return "", fmt.Errorf("agent %s starts %s, which is not installed (not found in PATH)", asked, program)
			}
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
func (a *App) saveAgentKinds(kinds []string, runs map[string][]string) error {
	b, err := json.Marshal(struct {
		Agents []string            `json:"agents"`
		Runs   map[string][]string `json:"runs,omitempty"`
	}{kinds, runs})
	if err != nil {
		return err
	}
	return paths.WritePrivateFile(a.agentKindsFile(), b)
}

func (a *App) loadAgentKinds() []string {
	agents, _ := a.loadSavedAgents()
	return agents
}

// agentRun is how Herdr desktop starts an agent, as the mod saved it from programs.json, or nil for its own name.
func (a *App) agentRun(kind string) []string {
	_, runs := a.loadSavedAgents()
	return runs[kind]
}

func (a *App) loadSavedAgents() ([]string, map[string][]string) {
	var saved struct {
		Agents []string            `json:"agents"`
		Runs   map[string][]string `json:"runs"`
	}
	if b, err := os.ReadFile(a.agentKindsFile()); err != nil || json.Unmarshal(b, &saved) != nil {
		return nil, nil
	}
	return saved.Agents, saved.Runs
}
