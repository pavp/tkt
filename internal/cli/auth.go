package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/jira"
)

const atlassianTokenURL = "https://id.atlassian.com/manage-profile/security/api-tokens"

func newAuthCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Jira and GitHub credentials",
	}
	cmd.AddCommand(newAuthJiraCmd(a), newAuthStatusCmd(a))
	return cmd
}

func newAuthJiraCmd(a *App) *cobra.Command {
	var site, email string
	cmd := &cobra.Command{
		Use:   "jira",
		Short: "Store a Jira API token in the system credential store",
		Long: "Store a Jira API token in the system credential store (macOS Keychain, Secret Service, or Windows\n" +
			"Credential Manager under WSL). In a terminal it opens " + atlassianTokenURL + ";\n" +
			"press Enter once the new token is copied and tkt reads it from the clipboard (asking first, and clearing\n" +
			"the clipboard once the token is saved), or paste it at the hidden prompt. When piped, it reads stdin.\n" +
			"A missing site or email is asked for, suggesting the last login or the git email.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, e, err := a.jiraAccount(cmd.Context(), site, email)
			if err != nil {
				return err
			}
			token, fromClipboard, err := a.readToken(cmd.Context(), e)
			if err != nil {
				return err
			}
			user, err := a.NewJira(s, e, token).Myself(cmd.Context())
			if errors.Is(err, jira.ErrUnauthorized) {
				return errors.New("jira rejected this token for " + e + "; nothing was saved")
			}
			verified := err == nil
			if !verified {
				fmt.Fprintf(a.Stderr, "! could not reach Jira to check the token (%v); saving it anyway\n", err)
			}
			if err := a.Creds.Set(credstore.JiraKey(s, e), token); err != nil {
				return err
			}
			if fromClipboard {
				a.clearClipboardIf(cmd.Context(), token)
			}
			st := a.loadState()
			st.JiraSite, st.JiraEmail = s, e
			a.saveState(st)
			if a.jsonOut {
				return a.printJSON(map[string]any{"saved": true, "verified": verified, "user": user.DisplayName, "store": a.Creds.Name(), "site": s, "email": e})
			}
			if verified {
				a.printf("✓ Connected to %s as %s\n", s, user.DisplayName)
			}
			a.printf("✓ Jira token saved in %s for %s on %s\n", a.Creds.Name(), e, s)
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "Jira site, e.g. example.atlassian.net (default: jira.site from the config)")
	cmd.Flags().StringVar(&email, "email", "", "Atlassian account email (default: jira.email from the config)")
	return cmd
}

// jiraAccount resolves the site and email from flags, the configuration and the last login; in a terminal
// it asks for what is still missing, suggesting the git email.
func (a *App) jiraAccount(ctx context.Context, site, email string) (string, string, error) {
	if site == "" || email == "" {
		c, err := a.LoadConfig()
		if err != nil && !errors.Is(err, config.ErrNotConfigured) {
			return "", "", err
		}
		if c != nil {
			site, email = cmp.Or(site, c.Jira.Site), cmp.Or(email, c.Jira.Email)
		}
		st := a.loadState()
		site, email = cmp.Or(site, st.JiraSite), cmp.Or(email, st.JiraEmail)
	}
	if (site == "" || email == "") && a.IsTerminal() {
		var err error
		if site == "" {
			if site, err = a.ask("Jira site (e.g. example.atlassian.net)", ""); err != nil {
				return "", "", err
			}
		}
		if email == "" {
			if email, err = a.ask("Atlassian account email", a.gitEmail(ctx)); err != nil {
				return "", "", err
			}
		}
	}
	if site == "" || email == "" {
		return "", "", errors.New("jira site and email are unknown: pass --site and --email, or run `tkt init`")
	}
	s, err := config.SiteURL(site)
	if err != nil {
		return "", "", err
	}
	return s, email, nil
}

// gitEmail is only a suggestion: git may hold a personal address while Jira uses the work one.
func (a *App) gitEmail(ctx context.Context) string {
	out, err := a.Run.Run(ctx, "", "git", []string{"config", "user.email"}, nil)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readToken opens Atlassian's token page and takes the token typed or pasted at a hidden prompt, or, when
// the user just presses Enter, the one they copied in the browser. fromClipboard tells the caller to clear
// the clipboard once the token is saved; clearing earlier would lose it, since Atlassian shows it once.
func (a *App) readToken(ctx context.Context, email string) (token string, fromClipboard bool, err error) {
	if !a.IsTerminal() {
		line, err := a.readLine("")
		if err != nil {
			return "", false, errors.New("no token on stdin")
		}
		if line == "" {
			return "", false, errors.New("no token entered")
		}
		return line, false, nil
	}
	if a.openURL(ctx, atlassianTokenURL) == nil {
		fmt.Fprintln(a.Stderr, "Opened the Atlassian token page in your browser: create a token and copy it.")
	}
	fmt.Fprintf(a.Stderr, "Create a token at %s\nJira API token for %s: press Enter once it is copied, or paste it (input hidden): ", atlassianTokenURL, email)
	if token, err = a.hiddenLine(); err != nil || token != "" {
		return token, false, err
	}
	if clip := a.readClipboard(ctx); looksLikeToken(clip) {
		ans, err := a.ask(fmt.Sprintf("Use the token on the clipboard (%d characters)? (Y/n)", len(clip)), "y")
		if err != nil {
			return "", false, err
		}
		if !strings.HasPrefix(strings.ToLower(ans), "n") {
			return clip, true, nil
		}
	} else {
		fmt.Fprintln(a.Stderr, "No token on the clipboard.")
	}
	fmt.Fprint(a.Stderr, "Jira API token (input hidden): ")
	if token, err = a.hiddenLine(); err == nil && token == "" {
		err = errors.New("no token entered")
	}
	return token, false, err
}

func (a *App) hiddenLine() (string, error) {
	b, err := a.ReadPassword()
	fmt.Fprintln(a.Stderr)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func looksLikeToken(s string) bool {
	return len(s) >= 20 && !strings.ContainsFunc(s, unicode.IsSpace)
}

func (a *App) readClipboard(ctx context.Context) string {
	type tool struct {
		name string
		args []string
	}
	var tools []tool
	switch {
	case a.Env.GOOS == "darwin":
		tools = []tool{{"pbpaste", nil}}
	case credstore.IsWSL(a.Env.Getenv, os.ReadFile):
		tools = []tool{{"powershell.exe", []string{"-NoProfile", "-Command", "Get-Clipboard"}}}
	default:
		tools = []tool{{"wl-paste", []string{"-n"}}, {"xclip", []string{"-selection", "clipboard", "-o"}}, {"xsel", []string{"--clipboard", "--output"}}}
	}
	for _, t := range tools {
		if _, err := a.Run.LookPath(t.name); err != nil {
			continue
		}
		out, err := a.Run.Run(ctx, "", t.name, t.args, nil)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return ""
}

// clearClipboardIf empties the clipboard only while it still holds token, so it never wipes something
// the user copied afterwards.
func (a *App) clearClipboardIf(ctx context.Context, token string) {
	if a.readClipboard(ctx) != token {
		return
	}
	if err := a.copyToClipboard(ctx, ""); err != nil {
		fmt.Fprintf(a.Stderr, "! could not clear the clipboard (%v); clear it yourself\n", err)
		return
	}
	fmt.Fprintln(a.Stderr, "✓ Cleared the token from the clipboard")
}

type jiraStatus struct {
	Site   string `json:"site,omitempty"`
	Email  string `json:"email,omitempty"`
	Token  string `json:"token"`
	Source string `json:"source,omitempty"`
	Store  string `json:"store"`
}

type githubStatus struct {
	Owner   string `json:"owner"`
	Account string `json:"account"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

func newAuthStatusCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which credentials tkt can use, without printing them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.LoadConfig()
			if err != nil {
				return err
			}
			js := a.jiraStatus(c)
			gs := a.githubStatuses(cmd, c)
			if a.jsonOut {
				return a.printJSON(map[string]any{"jira": js, "github": gs})
			}
			if js.Token == "present" {
				a.printf("✓ jira    %s · %s · token from %s\n", js.Site, js.Email, js.Source)
			} else {
				a.printf("✗ jira    %s · %s · no token: run `tkt auth jira`\n", js.Site, js.Email)
			}
			for _, g := range gs {
				if g.OK {
					a.printf("✓ github  %s via gh account %s\n", g.Owner, g.Account)
				} else {
					a.printf("✗ github  %s via gh account %s · %s\n", g.Owner, g.Account, g.Error)
				}
			}
			return nil
		},
	}
}

func (a *App) jiraStatus(c *config.Config) jiraStatus {
	site, _ := config.SiteURL(c.Jira.Site)
	st := jiraStatus{Site: site, Email: c.Jira.Email, Token: "missing", Store: a.Creds.Name()}
	if _, src, err := credstore.JiraToken(a.Creds, a.Env.Getenv, site, c.Jira.Email); err == nil {
		st.Token = "present"
		if src == credstore.SourceStore {
			st.Source = a.Creds.Name()
		} else {
			st.Source = credstore.EnvJiraToken
		}
	}
	return st
}

func (a *App) githubStatuses(cmd *cobra.Command, c *config.Config) []githubStatus {
	owners := map[string]bool{}
	for _, r := range c.Repos() {
		owners[r.Owner()] = true
	}
	sorted := make([]string, 0, len(owners))
	for o := range owners {
		sorted = append(sorted, o)
	}
	sort.Strings(sorted)
	out := make([]githubStatus, 0, len(sorted))
	for _, owner := range sorted {
		account := c.GitHubAccount(owner)
		st := githubStatus{Owner: owner, Account: account, OK: true}
		if st.Account == "" {
			st.Account = "(active)"
		}
		if _, err := github.Token(cmd.Context(), a.Run, account); err != nil {
			st.OK = false
			st.Error = err.Error()
		}
		out = append(out, st)
	}
	return out
}
