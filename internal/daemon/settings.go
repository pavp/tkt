package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/ticket"
)

// StatusesFunc lists a space's Jira statuses.
type StatusesFunc func(ctx context.Context, space string) ([]board.Status, error)

// BoardsFunc lists a space's Jira scrum boards.
type BoardsFunc func(ctx context.Context, space string) ([]config.TeamBoard, error)

// FieldsFunc lists the names of the Jira site's fields.
type FieldsFunc func(ctx context.Context) ([]string, error)

// FolderList is the folders inside a folder: Dir is that folder as config.toml writes it ("~/src/"), Broken the
// symlinks there whose target is gone.
type FolderList struct {
	Dir     string   `json:"dir"`
	Folders []string `json:"folders"`
	Broken  []string `json:"broken"`
}

// FoldersFunc lists the folders inside path, "~" or absolute.
type FoldersFunc func(path string) (FolderList, error)

// ReposFunc lists a GitHub owner's repositories as "owner/name".
type ReposFunc func(ctx context.Context, owner string) ([]string, error)

var githubOwner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// SettingsView is what the TUI's Settings shows from config.toml, with the choices that need no Jira.
type SettingsView struct {
	Spaces      []SpaceSettings `json:"spaces"`
	RepoRoots   []string        `json:"repo_roots"`
	LabelField  string          `json:"label_field"`
	PointsField string          `json:"points_field"`
	Palette     string          `json:"palette"`
	Columns     []ColumnChoice  `json:"columns"`
	Marks       []string        `json:"marks"`
}

type SpaceSettings struct {
	Key   string        `json:"key"`
	Name  string        `json:"name"`
	Repos []RepoSetting `json:"repos"`
	// Inferred are the repositories of the space's pull requests that are neither configured nor ignored.
	Inferred   []string      `json:"inferred"`
	TeamBoards []BoardChoice `json:"team_boards"`
}

type RepoSetting struct {
	Repo string `json:"repo"`
	Path string `json:"path"`
}

type BoardChoice struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ColumnChoice struct {
	ID    ticket.Category `json:"id"`
	Title string          `json:"title"`
}

type StatusSetting struct {
	Name       string `json:"name"`
	Category   string `json:"category"`
	Column     string `json:"column"`
	ColumnAuto bool   `json:"column_auto"`
	Mark       string `json:"mark"`
	MarkAuto   bool   `json:"mark_auto"`
}

type settingsPatch struct {
	RepoRoots   *[]string             `json:"repo_roots"`
	LabelField  *string               `json:"label_field"`
	PointsField *string               `json:"points_field"`
	Palette     *string               `json:"palette"`
	Spaces      map[string]spacePatch `json:"spaces"`
}

type spacePatch struct {
	Repos      *[]RepoSetting          `json:"repos"`
	Ignore     []string                `json:"ignore"`
	TeamBoards *[]int                  `json:"team_boards"`
	Statuses   map[string]*statusPatch `json:"statuses"`
}

type statusPatch struct {
	Column *string `json:"column"`
	Mark   *string `json:"mark"`
}

// shownPalette is the palette the mod uses: tkt's when chosen, Herdr's otherwise, a hand-written unknown value included.
func shownPalette(p string) string {
	if p == "tkt" {
		return p
	}
	return "herdr"
}

func markName(m ticket.Mark) string {
	if m == ticket.MarkNone {
		return "none"
	}
	return string(m)
}

func (s *Server) settingsView(c config.Config) SettingsView {
	s.mu.Lock()
	inferred := s.snap.Repos
	s.mu.Unlock()
	v := SettingsView{Spaces: []SpaceSettings{}, RepoRoots: orNoneOf(c.RepoRoots), LabelField: c.Jira.LabelField, PointsField: c.Jira.PointsField, Palette: shownPalette(c.Palette)}
	for _, sp := range c.Spaces {
		out := SpaceSettings{Key: sp.Key, Name: sp.Name, Repos: []RepoSetting{}, Inferred: orNoneOf(board.InferredRepos(sp, inferred[sp.Key])), TeamBoards: []BoardChoice{}}
		for _, r := range sp.Repos {
			out.Repos = append(out.Repos, RepoSetting{Repo: r.Name, Path: r.Path})
		}
		for _, b := range sp.TeamBoards {
			out.TeamBoards = append(out.TeamBoards, BoardChoice(b))
		}
		v.Spaces = append(v.Spaces, out)
	}
	for _, col := range ticket.Columns {
		v.Columns = append(v.Columns, ColumnChoice{ID: col, Title: col.Title()})
	}
	for _, m := range ticket.Marks {
		v.Marks = append(v.Marks, markName(m))
	}
	return v
}

func (s *Server) hasSettings(w http.ResponseWriter) bool {
	if s.opts.Settings == nil || s.opts.SaveSettings == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon serves no settings")
		return false
	}
	return true
}

func (s *Server) loadSettings(w http.ResponseWriter) (config.Config, bool) {
	c, err := s.opts.Settings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_failed", err.Error())
		return c, false
	}
	return c, true
}

func (s *Server) settings(w http.ResponseWriter, _ *http.Request) {
	if !s.hasSettings(w) {
		return
	}
	if c, ok := s.loadSettings(w); ok {
		writeJSON(w, http.StatusOK, s.settingsView(c))
	}
}

// settingsSpace reads the configured space a lookup's ?space= names.
func (s *Server) settingsSpace(w http.ResponseWriter, r *http.Request, canList bool) (config.Space, bool) {
	if !s.hasSettings(w) {
		return config.Space{}, false
	}
	if !canList {
		writeError(w, http.StatusNotFound, "not_found", "this daemon lists nothing from Jira for Settings")
		return config.Space{}, false
	}
	c, ok := s.loadSettings(w)
	if !ok {
		return config.Space{}, false
	}
	key := r.URL.Query().Get("space")
	sp, ok := c.Space(key)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("space must be a configured space, not %q", key))
	}
	return sp, ok
}

func (s *Server) settingsStatuses(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.settingsSpace(w, r, s.opts.JiraStatuses != nil)
	if !ok {
		return
	}
	statuses, err := s.opts.JiraStatuses(r.Context(), sp.Key)
	if err != nil {
		writeJiraError(w, err)
		return
	}
	out := []StatusSetting{}
	for _, st := range statuses {
		col, colAuto, mark, markAuto := board.StatusPlace(sp, st)
		out = append(out, StatusSetting{Name: st.Name, Category: st.Category, Column: string(col), ColumnAuto: colAuto, Mark: markName(mark), MarkAuto: markAuto})
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": sp.Key, "statuses": out})
}

func (s *Server) settingsBoards(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.settingsSpace(w, r, s.opts.JiraBoards != nil)
	if !ok {
		return
	}
	boards, err := s.opts.JiraBoards(r.Context(), sp.Key)
	if err != nil {
		writeJiraError(w, err)
		return
	}
	out := []BoardChoice{}
	for _, b := range boards {
		out = append(out, BoardChoice(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": sp.Key, "boards": out})
}

func (s *Server) settingsFields(w http.ResponseWriter, r *http.Request) {
	if !s.hasSettings(w) {
		return
	}
	if s.opts.JiraFields == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon lists nothing from Jira for Settings")
		return
	}
	names, err := s.opts.JiraFields(r.Context())
	if err != nil {
		writeJiraError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": orNoneOf(names)})
}

func (s *Server) settingsFolders(w http.ResponseWriter, r *http.Request) {
	if !s.hasSettings(w) {
		return
	}
	if s.opts.Folders == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon lists no folders for Settings")
		return
	}
	path := r.URL.Query().Get("path")
	// Only "~" and "~/" expand to the home; anything else, "~src" included, would be read from the daemon's own folder.
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "/") {
		writeError(w, http.StatusBadRequest, "bad_request", "give path, a folder starting with ~/ or /")
		return
	}
	list, err := s.opts.Folders(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "no folder "+path)
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", "cannot read "+path+": "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "dir": list.Dir, "folders": orNoneOf(list.Folders), "broken": orNoneOf(list.Broken)})
}

func (s *Server) settingsRepos(w http.ResponseWriter, r *http.Request) {
	if !s.hasSettings(w) {
		return
	}
	if s.opts.GitHubRepos == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon lists no repositories for Settings")
		return
	}
	owner := r.URL.Query().Get("owner")
	if !githubOwner.MatchString(owner) {
		writeError(w, http.StatusBadRequest, "bad_request", "give owner, a GitHub organization or user")
		return
	}
	names, err := s.opts.GitHubRepos(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusBadGateway, "github_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"owner": owner, "repos": orNoneOf(names)})
}

// badSettings is a change the TUI's Settings would refuse; it answers 400 and nothing is written.
type badSettings struct{ msg string }

func (e badSettings) Error() string { return e.msg }

func refuse(format string, args ...any) error { return badSettings{fmt.Sprintf(format, args...)} }

func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) {
	if !s.hasSettings(w) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	var patch settingsPatch
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&patch); err == nil && dec.Decode(&struct{}{}) != io.EOF {
			err = errors.New("more than one JSON value")
		}
	}
	if err == nil {
		var raw any
		_ = json.Unmarshal(body, &raw)
		err = noNulls(raw, "")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not a settings change: "+err.Error())
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	c, ok := s.loadSettings(w)
	if !ok {
		return
	}
	if err := s.applySettings(r.Context(), &c, patch); err != nil {
		if errors.As(err, new(badSettings)) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		} else {
			writeJiraError(w, err)
		}
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	saved, err := s.opts.SaveSettings(c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_failed", "not saved: "+err.Error())
		return
	}
	v := s.settingsView(saved)
	s.noteFile(s.opts.ConfigFile)
	s.mu.Lock()
	s.publish("settings.updated", v)
	s.mu.Unlock()
	s.syncSoon()
	writeJSON(w, http.StatusOK, v)
}

// noNulls refuses a null anywhere but as a status's change, where it brings the status back to automatic.
func noNulls(v any, path string) error {
	switch v := v.(type) {
	case nil:
		return fmt.Errorf("%s must not be null", path)
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if v[k] == nil && strings.HasSuffix(path, ".statuses") {
				continue
			}
			if err := noNulls(v[k], strings.TrimPrefix(path+"."+k, ".")); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range v {
			if err := noNulls(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// applySettings makes the changes the TUI's Settings makes, with its checks; config.Validate checks the rest.
func (s *Server) applySettings(ctx context.Context, c *config.Config, p settingsPatch) error {
	if p.RepoRoots != nil {
		roots := make([]string, len(*p.RepoRoots))
		for i, root := range *p.RepoRoots {
			if roots[i] = strings.TrimSpace(root); roots[i] == "" {
				return refuse("repo_roots: Folder is required")
			}
		}
		c.RepoRoots = roots
	}
	if p.LabelField != nil {
		c.Jira.LabelField = strings.TrimSpace(*p.LabelField)
	}
	if p.PointsField != nil {
		c.Jira.PointsField = strings.TrimSpace(*p.PointsField)
	}
	if p.Palette != nil {
		switch v := strings.TrimSpace(*p.Palette); v {
		case "tkt":
			c.Palette = v
		case "herdr", "":
			c.Palette = ""
		default:
			return refuse("unknown palette %q (use herdr or tkt)", v)
		}
	}
	s.mu.Lock()
	inferred := s.snap.Repos
	s.mu.Unlock()
	for _, key := range slices.Sorted(maps.Keys(p.Spaces)) {
		i := slices.IndexFunc(c.Spaces, func(sp config.Space) bool { return sp.Key == key })
		if i < 0 {
			return refuse("unknown space %q", key)
		}
		if err := s.applySpace(ctx, &c.Spaces[i], p.Spaces[key], inferred[key]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applySpace(ctx context.Context, sp *config.Space, p spacePatch, inferred []string) error {
	shown := board.InferredRepos(*sp, inferred)
	for _, name := range p.Ignore {
		if !slices.Contains(shown, name) {
			return refuse("%s is not a repository inferred for %s", name, sp.Key)
		}
	}
	if p.Repos != nil {
		repos := make([]config.Repo, len(*p.Repos))
		for i, r := range *p.Repos {
			name := strings.TrimSpace(r.Repo)
			if name == "" {
				return refuse("%s: Repository (owner/name) is required", sp.Key)
			}
			repos[i] = config.Repo{Name: name, Path: strings.TrimSpace(r.Path)}
			if j := slices.IndexFunc(sp.Repos, func(old config.Repo) bool { return strings.EqualFold(old.Name, name) }); j >= 0 {
				repos[i].Base = sp.Repos[j].Base
			}
		}
		sp.Repos = repos
	}
	sp.Ignored = append(sp.Ignored, p.Ignore...)
	if p.TeamBoards != nil {
		if err := s.setTeamBoards(ctx, sp, *p.TeamBoards); err != nil {
			return err
		}
	}
	if len(p.Statuses) > 0 {
		return s.setStatuses(ctx, sp, p.Statuses)
	}
	return nil
}

// setTeamBoards keeps the boards by id in the order sent; a board not yet saved must be one of the space's scrum
// boards, which Jira is asked for only then.
func (s *Server) setTeamBoards(ctx context.Context, sp *config.Space, ids []int) error {
	var jira []config.TeamBoard
	out := make([]config.TeamBoard, 0, len(ids))
	for _, id := range ids {
		i := slices.IndexFunc(sp.TeamBoards, func(b config.TeamBoard) bool { return b.ID == id })
		var b config.TeamBoard
		switch {
		case i >= 0:
			b = sp.TeamBoards[i]
		case s.opts.JiraBoards == nil:
			return refuse("this daemon cannot list the boards of %s", sp.Key)
		default:
			if jira == nil {
				var err error
				if jira, err = s.opts.JiraBoards(ctx, sp.Key); err != nil {
					return err
				}
			}
			j := slices.IndexFunc(jira, func(b config.TeamBoard) bool { return b.ID == id })
			if j < 0 {
				return refuse("no scrum board of %s has the id %d", sp.Key, id)
			}
			b = jira[j]
		}
		if slices.ContainsFunc(out, func(t config.TeamBoard) bool { return t.ID == id }) {
			return refuse("%s is already a team board of %s", b.Name, sp.Key)
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		out = nil
	}
	sp.TeamBoards = out
	return nil
}

func (s *Server) setStatuses(ctx context.Context, sp *config.Space, changes map[string]*statusPatch) error {
	if s.opts.JiraStatuses == nil {
		return refuse("this daemon cannot list the statuses of %s", sp.Key)
	}
	statuses, err := s.opts.JiraStatuses(ctx, sp.Key)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(changes)) {
		i := slices.IndexFunc(statuses, func(st board.Status) bool { return st.Name == name })
		if i < 0 {
			return refuse("%s has no status %q", sp.Key, name)
		}
		ch := changes[name]
		switch {
		case ch == nil:
			board.ResetStatus(sp, name)
			continue
		case ch.Column == nil && ch.Mark == nil:
			return refuse("%s: status %q needs a column, a mark, or null", sp.Key, name)
		}
		if ch.Column != nil {
			col, ok := ticket.ParseCategory(*ch.Column)
			if !ok {
				return refuse("%s: status %q has unknown column %q (use todo, in_progress, review or done)", sp.Key, name, *ch.Column)
			}
			board.SetStatusColumn(sp, statuses[i], col)
		}
		if ch.Mark != nil {
			mark, ok := ticket.ParseMark(*ch.Mark)
			if !ok {
				return refuse("%s: status %q has unknown mark %q (use none, diamond, blocked, clock, flag, star or triangle)", sp.Key, name, *ch.Mark)
			}
			board.SetStatusMark(sp, statuses[i], mark)
		}
	}
	return nil
}
