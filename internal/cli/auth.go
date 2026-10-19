package cli

import (
	"bufio"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/github"
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
			"Credential Manager under WSL). The token is read from a hidden prompt, or from stdin when piped.\n" +
			"Create one at " + atlassianTokenURL + ".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, e, err := a.jiraAccount(site, email)
			if err != nil {
				return err
			}
			token, err := a.readToken(e)
			if err != nil {
				return err
			}
			if err := a.Creds.Set(credstore.JiraKey(s, e), token); err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"saved": true, "store": a.Creds.Name(), "site": s, "email": e})
			}
			a.printf("✓ Jira token saved in %s for %s on %s\n", a.Creds.Name(), e, s)
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "Jira site, e.g. example.atlassian.net (default: jira.site from the config)")
	cmd.Flags().StringVar(&email, "email", "", "Atlassian account email (default: jira.email from the config)")
	return cmd
}

// jiraAccount resolves the site and email from flags, falling back to the configuration.
func (a *App) jiraAccount(site, email string) (string, string, error) {
	if site == "" || email == "" {
		c, err := a.LoadConfig()
		if err != nil && !errors.Is(err, config.ErrNotConfigured) {
			return "", "", err
		}
		if c != nil {
			if site == "" {
				site = c.Jira.Site
			}
			if email == "" {
				email = c.Jira.Email
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

func (a *App) readToken(email string) (string, error) {
	var raw string
	if a.IsTerminal() {
		fmt.Fprintf(a.Stderr, "Create a token at %s\nJira API token for %s (input hidden): ", atlassianTokenURL, email)
		b, err := a.ReadPassword()
		fmt.Fprintln(a.Stderr)
		if err != nil {
			return "", fmt.Errorf("read token: %w", err)
		}
		raw = string(b)
	} else {
		line, err := bufio.NewReader(a.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no token on stdin")
		}
		raw = line
	}
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", errors.New("no token entered")
	}
	return token, nil
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
