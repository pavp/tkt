package herdrcore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/herdrcore"
)

// runRequest decodes a `terminal.run` sent over `hc connect`; ok is false for any other call.
func runRequest(c call) (params map[string]any, ok bool) {
	var req struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if !reflect.DeepEqual(c.args, []string{"connect"}) || json.Unmarshal([]byte(c.stdin), &req) != nil || req.Method != "terminal.run" {
		return nil, false
	}
	return req.Params, true
}

// runArgs shows a `terminal.run` request as the `hc run` command line it stands for, and any other call as its args.
func runArgs(c call) []string {
	p, ok := runRequest(c)
	if !ok {
		return c.args
	}
	out := []string{"run", "--bg", "--json"}
	if p["no_parent"] == true {
		out = append(out, "--no-parent")
	}
	if p["rm"] == true {
		out = append(out, "--rm")
	}
	if n, _ := p["name"].(string); n != "" {
		out = append(out, "--name", n)
	}
	out = append(out, "--cwd", fmt.Sprint(p["cwd"]))
	labels, _ := p["labels"].(map[string]any)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		out = append(out, "--label", k+"="+fmt.Sprint(labels[k]))
	}
	if argv, _ := p["argv"].([]any); len(argv) > 0 {
		out = append(out, "--")
		for _, a := range argv {
			out = append(out, fmt.Sprint(a))
		}
	}
	return out
}

// herdr-core starts terminals with its own environment, which on another machine of the core may lack the PATH that
// finds hc and the agents, and the config tkt runs with; secrets never travel to it.
func TestTerminalsTktStartsGetItsEnvironment(t *testing.T) {
	environ := []string{"PATH=/home/dev/.local/bin:/usr/bin", "HOME=/home/dev", "XDG_CONFIG_HOME=/home/dev/cfg", "XDG_DATA_HOME=/home/dev/data",
		"TKT_E2E=1", "TKT_JIRA_TOKEN=s3cret", "TKT_jira_token=s3cret", "TKT_API_KEY=s3cret", "TKT_GH_PAT=s3cret", "TKT_DB_PASSWD=s3cret", "TKT_CREDENTIALS=s3cret", "TKT_EMPTY=", "TERM=xterm", "HERDR_APP_HOME=/home/dev/cfg/herdr-app"}
	want := map[string]any{"PATH": "/home/dev/.local/bin:/usr/bin", "XDG_CONFIG_HOME": "/home/dev/cfg", "XDG_DATA_HOME": "/home/dev/data", "TKT_E2E": "1", "HERDR_APP_HOME": "/home/dev/cfg/herdr-app"}
	ctx := context.Background()
	tests := []struct {
		name string
		outs []string
		act  func(c *herdrcore.Client) error
	}{
		{"the detached closer", []string{ran}, func(c *herdrcore.Client) error {
			return c.RunDetached(ctx, "/home/dev/data/tkt", []string{"sh", "-c", "exit 0"})
		}},
		{"a space's shell", []string{ran}, func(c *herdrcore.Client) error {
			_, _, err := c.CreateWorkspace(ctx, "/home/dev/code/web", "", false)
			return err
		}},
		{"the handoff agent", []string{ran, ran}, func(c *herdrcore.Client) error {
			_, _, err := c.OpenAgentSpace(ctx, "/home/dev/code/web-DEMO-1", "", "claude", "Work on DEMO-1")
			return err
		}},
		{"the setup agent", []string{got("t3", "null", placed), ran, got("t12", status("idle"), "{}")}, func(c *herdrcore.Client) error {
			return c.StartAgent(ctx, "t3", "claude", "claude", nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &seqHC{outs: tt.outs}
			c := idsClient(hc, &fakeApp{})
			herdrcore.SetEnviron(c, func() []string { return environ })
			if err := tt.act(c); err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(hc.calls, func(c call) bool { _, ok := runRequest(c); return ok })
			if i < 0 {
				t.Fatalf("no terminal.run in %+v", hc.calls)
			}
			p, _ := runRequest(hc.calls[i])
			if !reflect.DeepEqual(p["env"], want) {
				t.Errorf("env = %v\nwant  %v", p["env"], want)
			}
			if strings.Contains(hc.calls[i].stdin, "s3cret") {
				t.Errorf("a secret reached herdr-core: %s", hc.calls[i].stdin)
			}
		})
	}
}

func TestAnAgentsTerminalGetsTheInteractiveShellsPath(t *testing.T) {
	c := herdrcore.NewWith(&seqHC{})
	herdrcore.SetEnviron(c, func() []string { return []string{"PATH=/usr/bin:/home/u/.local/bin", "TKT_PROFILE=work"} })
	c.PathFrom(func() string { return "/home/linuxbrew/.linuxbrew/bin:/usr/bin" })
	env := herdrcore.Env(c)
	if want := "/home/linuxbrew/.linuxbrew/bin:/usr/bin:/home/u/.local/bin"; env["PATH"] != want {
		t.Errorf("PATH = %q, want %q: the shell's PATH first (as Herdr starts agents), then tkt's own", env["PATH"], want)
	}
	if env["TKT_PROFILE"] != "work" {
		t.Errorf("env = %v, lost tkt's own variables", env)
	}
	c.PathFrom(func() string { return "" })
	if env := herdrcore.Env(c); env["PATH"] != "/usr/bin:/home/u/.local/bin" {
		t.Errorf("PATH without a shell answer = %q, want tkt's own", env["PATH"])
	}
}
