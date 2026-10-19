package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/jira"
)

func newInitCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Set up tkt: Jira account and token, spaces, repositories and GitHub accounts",
		Long: "Set up tkt interactively. It asks for the Jira site and email, stores and checks the API token,\n" +
			"reads your spaces and their statuses from Jira, and writes ~/.config/tkt/config.toml. You only\n" +
			"type what Jira cannot know: which repositories each space uses and where your clones live.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.jsonOut {
				return errInteractive("init")
			}
			return a.runInit(cmd)
		},
	}
}

// ask reads one answer, falling back to def on an empty line; running out of input is an error.
func (a *App) ask(prompt, def string) (string, error) {
	if def != "" {
		prompt = fmt.Sprintf("%s [%s]: ", prompt, def)
	} else {
		prompt += ": "
	}
	ans, err := a.readLine(prompt)
	if errors.Is(err, io.EOF) {
		return "", errors.New("setup needs answers: run `tkt init` in a terminal")
	}
	if err != nil {
		return "", err
	}
	if ans == "" {
		return def, nil
	}
	return ans, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) runInit(cmd *cobra.Command) error {
	ctx := cmd.Context()
	path := a.ConfigPath()
	prev, _ := a.LoadConfig()
	if _, err := os.Stat(path); err == nil {
		ans, err := a.ask("A config already exists at "+path+". Replace it? (y/N)", "n")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToLower(ans), "y") {
			return errors.New("kept the existing config")
		}
	}
	defSite, defEmail := "", ""
	if prev != nil {
		defSite, defEmail = prev.Jira.Site, prev.Jira.Email
	} else if st := a.loadState(); st.JiraSite != "" {
		defSite, defEmail = st.JiraSite, st.JiraEmail
	}
	fmt.Fprintln(a.Stderr, "tkt setup · Ctrl-C cancels at any time; nothing is saved until the end")

	fmt.Fprintln(a.Stderr, "\n1/4 Jira")
	var siteURL, email, token string
	if defSite != "" && defEmail != "" {
		if u, err := config.SiteURL(defSite); err == nil {
			if t, _, err := credstore.JiraToken(a.Creds, a.Env.Getenv, u, defEmail); err == nil {
				siteURL, email, token = u, defEmail, t
				fmt.Fprintf(a.Stderr, "Using %s as %s with the stored token (another account: `tkt auth jira --site … --email …` first)\n", u, defEmail)
			}
		}
	}
	if token == "" {
		site, err := a.ask("Jira site (e.g. example.atlassian.net)", defSite)
		if err != nil {
			return err
		}
		if siteURL, err = config.SiteURL(site); err != nil {
			return err
		}
		if email, err = a.ask("Atlassian account email", defEmail); err != nil {
			return err
		}
		if email == "" {
			return errors.New("an email is required")
		}
		if t, src, err := credstore.JiraToken(a.Creds, a.Env.Getenv, siteURL, email); err == nil {
			question := "Use the token already stored for " + email + "? (Y/n)"
			if src == credstore.SourceEnv {
				question = "Use the token in " + credstore.EnvJiraToken + " and save it in " + a.Creds.Name() + "? (Y/n)"
			}
			ans, err := a.ask(question, "y")
			if err != nil {
				return err
			}
			if !strings.HasPrefix(strings.ToLower(ans), "n") {
				token = t
			}
		}
		if token == "" {
			if token, err = a.readToken(email); err != nil {
				return err
			}
		}
	}
	js := a.NewJira(siteURL, email, token)
	user, err := js.Myself(ctx)
	if errors.Is(err, jira.ErrUnauthorized) {
		return errors.New("jira rejected this token; nothing was saved")
	}
	if err != nil {
		return fmt.Errorf("could not reach Jira: %w", err)
	}
	if err := a.Creds.Set(credstore.JiraKey(siteURL, email), token); err != nil {
		return err
	}
	fmt.Fprintf(a.Stderr, "✓ Connected as %s; token saved in %s\n\n", user.DisplayName, a.Creds.Name())

	fmt.Fprintln(a.Stderr, "2/4 Spaces")
	projects, err := js.Projects(ctx)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return errors.New("your Jira account cannot browse any space")
	}
	for i, p := range projects {
		fmt.Fprintf(a.Stderr, "  %d) %-10s %s\n", i+1, p.Key, p.Name)
	}
	picked, err := a.ask("Spaces to include (numbers or keys, comma separated)", "")
	if err != nil {
		return err
	}
	var spaces []config.Space
	for _, choice := range splitList(picked) {
		p, ok := pickProject(projects, choice)
		if !ok {
			return fmt.Errorf("%q is not one of the listed spaces", choice)
		}
		spaces = append(spaces, config.Space{Key: p.Key, Name: p.Name})
	}
	if len(spaces) == 0 {
		return errors.New("pick at least one space")
	}

	fmt.Fprintln(a.Stderr, "\n3/4 Repositories")
	owners := map[string]bool{}
	var ownerOrder []string
	for i := range spaces {
		s := &spaces[i]
		ans, err := a.ask("Repositories for "+s.Key+" (owner/name, comma separated, empty for none)", "")
		if err != nil {
			return err
		}
		if len(splitList(ans)) == 0 {
			fmt.Fprintf(a.Stderr, "! %s has no repositories: its pull requests and `tkt start` need at least one (add them later in the config)\n", s.Key)
		}
		for _, name := range splitList(ans) {
			r := config.Repo{Name: name}
			s.Repos = append(s.Repos, r)
			if o := r.Owner(); !owners[o] {
				owners[o] = true
				ownerOrder = append(ownerOrder, o)
			}
		}
		statuses, err := js.Statuses(ctx, s.Key)
		if err != nil {
			fmt.Fprintf(a.Stderr, "! statuses of %s unavailable: %v\n", s.Key, err)
			continue
		}
		for _, st := range statuses {
			if st.Category == "indeterminate" && strings.Contains(strings.ToLower(st.Name), "review") {
				if s.StatusOverrides == nil {
					s.StatusOverrides = map[string]string{}
				}
				s.StatusOverrides[st.Name] = "review"
				fmt.Fprintf(a.Stderr, "  %s: %q → In Review column\n", s.Key, st.Name)
			}
		}
	}
	roots, err := a.ask("Folders with your local clones (comma separated)", "~/dev")
	if err != nil {
		return err
	}

	fmt.Fprintln(a.Stderr, "\n4/4 GitHub")
	accounts := map[string]string{}
	for _, o := range ownerOrder {
		acct, err := a.ask("gh account that reads "+o+" (empty = gh's active account)", "")
		if err != nil {
			return err
		}
		if acct != "" {
			accounts[o] = acct
		}
		if _, err := github.Token(ctx, a.Run, acct); err != nil {
			fmt.Fprintf(a.Stderr, "! %v\n", err)
		} else {
			fmt.Fprintf(a.Stderr, "✓ GitHub access for %s\n", o)
		}
	}

	c := &config.Config{
		RepoRoots: splitList(roots),
		Jira:      config.Jira{Site: strings.TrimPrefix(siteURL, "https://"), Email: email},
		Spaces:    spaces,
	}
	if len(accounts) > 0 {
		c.GitHub.Accounts = accounts
	}
	if err := config.Save(path, c); err != nil {
		return err
	}
	a.printf("✓ Wrote %s\nNext: `tkt doctor` checks everything, then `tkt list`.\n", path)
	if a.Env.Getenv("HERDR_PLUGIN_ID") != "" {
		// Herdr closes a plugin pane as soon as its command exits, which would hide this summary.
		if _, err := a.readLine("\nPress Enter to open the board "); err == nil {
			_ = a.NewHerdr().OpenPluginPane(cmd.Context(), herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: "board"})
		}
	}
	return nil
}

func pickProject(projects []jira.Project, choice string) (jira.Project, bool) {
	if n, err := strconv.Atoi(choice); err == nil {
		if n >= 1 && n <= len(projects) {
			return projects[n-1], true
		}
		return jira.Project{}, false
	}
	for _, p := range projects {
		if strings.EqualFold(p.Key, choice) {
			return p, true
		}
	}
	return jira.Project{}, false
}
