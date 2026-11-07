package cli

import (
	"context"
	"path/filepath"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/daemon"
)

// savedConfig reads config.toml as written, "~" unexpanded, so Settings saves the user's paths portable.
func (a *App) savedConfig() (config.Config, error) {
	c, err := config.Load(a.ConfigPath())
	if err != nil {
		return config.Config{}, err
	}
	return *c, nil
}

// saveConfig writes Settings' change to config.toml and returns it as saved, defaults applied.
func (a *App) saveConfig(c config.Config) (config.Config, error) {
	c = c.Clone()
	if err := config.Save(a.ConfigPath(), &c); err != nil {
		return config.Config{}, err
	}
	return c, nil
}

func (a *App) jiraStatuses(ctx context.Context, c *config.Config, space string) ([]board.Status, error) {
	js, err := a.jiraClient(c)
	if err != nil {
		return nil, err
	}
	statuses, err := js.Statuses(ctx, space)
	out := make([]board.Status, len(statuses))
	for i, st := range statuses {
		out[i] = board.Status{Name: st.Name, Category: st.Category}
	}
	return out, err
}

func (a *App) jiraBoards(ctx context.Context, c *config.Config, space string) ([]config.TeamBoard, error) {
	js, err := a.jiraClient(c)
	if err != nil {
		return nil, err
	}
	return a.teamBoardChoices(ctx, js, space)
}

func (a *App) jiraFieldNames(ctx context.Context, c *config.Config) ([]string, error) {
	js, err := a.jiraClient(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return js.FieldNames(ctx)
}

// daemonStatuses, daemonBoards and daemonFields serve Settings' Jira lists with the configuration read afresh, like each sync.
func (a *App) daemonStatuses(ctx context.Context, space string) ([]board.Status, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	return a.jiraStatuses(ctx, c, space)
}

func (a *App) daemonBoards(ctx context.Context, space string) ([]config.TeamBoard, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	return a.jiraBoards(ctx, c, space)
}

func (a *App) daemonFields(ctx context.Context) ([]string, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	return a.jiraFieldNames(ctx, c)
}

// settingsFolders and settingsRepos serve Settings' completion of folders and repositories, as the TUI lists them.
func (a *App) settingsFolders(path string) (daemon.FolderList, error) {
	abs := filepath.Clean(a.Env.ExpandHome(path))
	names, broken, err := listDirs(abs)
	if err != nil {
		return daemon.FolderList{}, err
	}
	return daemon.FolderList{Dir: withSlash(a.tildePath(abs)), Folders: names, Broken: broken}, nil
}

func (a *App) settingsRepos(ctx context.Context, owner string) ([]string, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return a.NewGitHub(c).Repos(ctx, owner)
}
