package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
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
		Short: "Set up tkt: Jira account and token, spaces, repositories, team boards and GitHub accounts",
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
	fromClipboard := false
	if defSite != "" && defEmail != "" {
		if u, err := config.SiteURL(defSite); err == nil {
			if t, _, err := credstore.JiraToken(a.Creds, a.Env.Getenv, u, defEmail); err == nil {
				siteURL, email, token = u, defEmail, t
				fmt.Fprintf(a.Stderr, "Using %s as %s with the stored token (another account: `tkt auth jira --site … --email …` first)\n", u, defEmail)
			}
		}
	}
	if token == "" {
		site, err := a.ask("Jira site, e.g. example.atlassian.net (or paste any Jira link)", defSite)
		if err != nil {
			return err
		}
		if siteURL, err = config.SiteURL(site); err != nil {
			return err
		}
		if defEmail == "" && a.IsTerminal() {
			defEmail = a.gitEmail(ctx)
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
			if token, fromClipboard, err = a.readToken(ctx, email); err != nil {
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
	if err := a.saveJiraToken(siteURL, email, token); err != nil {
		return err
	}
	if fromClipboard {
		a.clearClipboardIf(ctx, token)
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

	fmt.Fprintln(a.Stderr, "\n3/4 Repositories and team boards")
	owners := map[string]bool{}
	var ownerOrder []string
	for i := range spaces {
		s := &spaces[i]
		ans, err := a.ask("Repositories for "+s.Key+" (owner/name, comma separated)", strings.Join(suggestRepos(ctx, js, s.Key), ", "))
		if err != nil {
			return err
		}
		if len(splitList(ans)) == 0 {
			fmt.Fprintf(a.Stderr, "! %s has no repositories yet: tkt infers them from the pull requests Jira links to your tickets\n", s.Key)
		}
		for _, name := range splitList(ans) {
			r := config.Repo{Name: name}
			s.Repos = append(s.Repos, r)
			if o := r.Owner(); !owners[o] {
				owners[o] = true
				ownerOrder = append(ownerOrder, o)
			}
		}
		if err := a.askTeamBoards(ctx, js, s); err != nil {
			return err
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
	logged := github.LoggedInAccounts(ctx, a.Run)
	if len(logged) == 0 {
		fmt.Fprintln(a.Stderr, "! gh has no logged-in GitHub account: pull requests, CI and reviews need one")
		if logged, err = a.offerGhLogin(ctx); err != nil {
			return err
		}
	}
	accounts := map[string]string{}
	for _, o := range ownerOrder {
		acct, err := a.pickAccount(ctx, logged, o)
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

// askTeamBoards asks which of the space's scrum boards make the board's Team mode, by name, when it has any.
func (a *App) askTeamBoards(ctx context.Context, js JiraAPI, s *config.Space) error {
	boards, err := js.Boards(ctx, s.Key)
	if err != nil {
		fmt.Fprintf(a.Stderr, "! boards of %s unavailable: %v\n", s.Key, err)
		return nil
	}
	if len(boards) == 0 {
		return nil
	}
	ans, err := a.ask(fmt.Sprintf("Team boards for %s (names, comma separated; %d scrum boards)", s.Key, len(boards)), "")
	if err != nil {
		return err
	}
	for _, name := range splitList(ans) {
		b, n := matchBoard(boards, name)
		switch {
		case n == 0:
			fmt.Fprintf(a.Stderr, "! no scrum board of %s is called %q\n", s.Key, name)
		case n > 1:
			fmt.Fprintf(a.Stderr, "! %q fits several boards of %s: type more of its name\n", name, s.Key)
		case !slices.ContainsFunc(s.TeamBoards, func(t config.TeamBoard) bool { return t.ID == b.ID }):
			s.TeamBoards = append(s.TeamBoards, config.TeamBoard{ID: b.ID, Name: b.Name})
			fmt.Fprintf(a.Stderr, "  %s team board: %s\n", s.Key, b.Name)
		}
	}
	return nil
}

// matchBoard finds the board called name, ignoring case, else the boards whose name contains it; n says
// how many matched, so a name that fits several is refused rather than guessed.
func matchBoard(boards []jira.Board, name string) (jira.Board, int) {
	for _, b := range boards {
		if strings.EqualFold(b.Name, name) {
			return b, 1
		}
	}
	var found []jira.Board
	for _, b := range boards {
		if strings.Contains(strings.ToLower(b.Name), strings.ToLower(name)) {
			found = append(found, b)
		}
	}
	if len(found) == 1 {
		return found[0], 1
	}
	return jira.Board{}, len(found)
}

// suggestRepos lists the repositories holding the pull requests Jira links to the user's sprint tickets
// in space, so setup can propose them.
func suggestRepos(ctx context.Context, js JiraAPI, space string) []string {
	issues, err := js.Search(ctx, jira.MySprintJQL([]string{space}), jira.ListFields)
	if err != nil {
		return nil
	}
	var out []string
	for i, is := range issues {
		if i == 20 {
			break
		}
		prs, err := js.DevPullRequests(ctx, is.ID)
		if err != nil {
			return out
		}
		for _, p := range prs {
			if r := strings.ToLower(p.Repo); r != "" && !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (a *App) offerGhLogin(ctx context.Context) ([]string, error) {
	ans, err := a.ask("Log in to GitHub with gh now? (Y/n)", "y")
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.ToLower(ans), "n") {
		return nil, nil
	}
	if err := a.Launch("", "gh", []string{"auth", "login"}); err != nil {
		fmt.Fprintf(a.Stderr, "! gh auth login: %v\n", err)
	}
	return github.LoggedInAccounts(ctx, a.Run), nil
}

// pickAccount chooses the gh account for owner: the only one that can read it, or the user's pick when
// several can. With none, it offers a login once, then falls back to asking.
func (a *App) pickAccount(ctx context.Context, logged []string, owner string) (string, error) {
	readers := a.readers(ctx, logged, owner)
	if len(readers) == 0 && len(logged) > 0 {
		fmt.Fprintf(a.Stderr, "! none of your gh accounts (%s) can read %s\n", strings.Join(logged, ", "), owner)
		more, err := a.offerGhLogin(ctx)
		if err != nil {
			return "", err
		}
		readers = a.readers(ctx, more, owner)
	}
	switch len(readers) {
	case 1:
		fmt.Fprintf(a.Stderr, "✓ %s via gh account %s\n", owner, readers[0])
		return readers[0], nil
	case 0:
		return a.ask("gh account that reads "+owner+" (empty = gh's active account)", "")
	}
	ans, err := a.ask(fmt.Sprintf("gh account for %s (%s)", owner, strings.Join(readers, ", ")), readers[0])
	if err != nil {
		return "", err
	}
	return ans, nil
}
