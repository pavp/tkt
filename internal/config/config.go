package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/ticket"
)

var ErrNotConfigured = errors.New("tkt is not configured yet: run `tkt init`")

type Config struct {
	RepoRoots []string `toml:"repo_roots,omitempty"`
	// Reviews selects which review requests the board shows: direct (default), team, or off.
	Reviews string  `toml:"reviews,omitempty"`
	Jira    Jira    `toml:"jira"`
	GitHub  GitHub  `toml:"github,omitempty"`
	Context Context `toml:"context,omitempty"`
	Spaces  []Space `toml:"spaces"`
}

type Jira struct {
	Site  string `toml:"site"`
	Email string `toml:"email"`
}

type GitHub struct {
	// Accounts maps a GitHub organization or user to the gh account whose token reads it.
	Accounts map[string]string `toml:"accounts,omitempty"`
}

type Context struct {
	Comments int `toml:"comments,omitempty"`
}

type Space struct {
	Key             string            `toml:"key"`
	Name            string            `toml:"name,omitempty"`
	AcceptanceField string            `toml:"acceptance_field,omitempty"`
	StatusOverrides map[string]string `toml:"status_overrides,omitempty"`
	Repos           []Repo            `toml:"repos,omitempty"`
	// Ignored lists repositories inferred from Jira that tkt must not use; a configured one still counts.
	Ignored []string `toml:"ignored,omitempty"`
}

// Ignores reports whether repo ("owner/name", any case) is on the space's ignored list.
func (s Space) Ignores(repo string) bool {
	return slices.ContainsFunc(s.Ignored, func(r string) bool { return strings.EqualFold(r, repo) })
}

type Repo struct {
	Name string `toml:"repo"`
	// Base is accepted for older configs; nothing reads it, since agents pick base branches per project.
	Base string `toml:"base,omitempty"`
	Path string `toml:"path,omitempty"`
}

const DefaultComments = 5

const (
	ReviewsDirect = "direct"
	ReviewsTeam   = "team"
	ReviewsOff    = "off"
)

// ReviewMode returns the configured review mode, defaulting to direct requests only.
func (c *Config) ReviewMode() string {
	if c.Reviews == "" {
		return ReviewsDirect
	}
	return c.Reviews
}

var (
	spaceKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// Load reads and validates the configuration file, applying defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("parse %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &c, nil
}

// Save validates c and writes it with mode 0600.
func Save(path string, c *Config) error {
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return paths.WritePrivateFile(path, buf.Bytes())
}

func (c *Config) applyDefaults() {
	if c.Context.Comments == 0 {
		c.Context.Comments = DefaultComments
	}

}

func (c *Config) Validate() error {
	var errs []error
	if c.Jira.Site == "" {
		errs = append(errs, errors.New("jira.site is required"))
	} else if _, err := SiteURL(c.Jira.Site); err != nil {
		errs = append(errs, err)
	}
	if c.Jira.Email == "" {
		errs = append(errs, errors.New("jira.email is required"))
	}
	switch c.Reviews {
	case "", ReviewsDirect, ReviewsTeam, ReviewsOff:
	default:
		errs = append(errs, fmt.Errorf("reviews = %q must be direct, team or off", c.Reviews))
	}
	if c.Context.Comments < 0 {
		errs = append(errs, errors.New("context.comments must not be negative"))
	}
	seen := map[string]bool{}
	for _, s := range c.Spaces {
		if !spaceKeyPattern.MatchString(s.Key) {
			errs = append(errs, fmt.Errorf("space key %q must be uppercase letters and digits, like DEMO", s.Key))
		}
		if seen[s.Key] {
			errs = append(errs, fmt.Errorf("space %s is listed twice", s.Key))
		}
		seen[s.Key] = true
		for status, cat := range s.StatusOverrides {
			if _, ok := ticket.ParseCategory(cat); !ok {
				errs = append(errs, fmt.Errorf("space %s: status %q maps to unknown column %q (use todo, in_progress, review or done)", s.Key, status, cat))
			}
		}
		for _, r := range s.Repos {
			if !repoPattern.MatchString(r.Name) {
				errs = append(errs, fmt.Errorf("space %s: repo %q must look like owner/name", s.Key, r.Name))
			}
		}
	}
	return errors.Join(errs...)
}

// SiteURL normalizes a Jira site such as "example.atlassian.net" to "https://example.atlassian.net".
func SiteURL(site string) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(site), "/")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("jira.site %q is not a valid URL", site)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("jira.site %q must use https", site)
	}
	return "https://" + u.Host, nil
}

func (c *Config) Space(key string) (Space, bool) {
	for _, s := range c.Spaces {
		if s.Key == key {
			return s, true
		}
	}
	return Space{}, false
}

func (c *Config) SpaceKeys() []string {
	keys := make([]string, 0, len(c.Spaces))
	for _, s := range c.Spaces {
		keys = append(keys, s.Key)
	}
	return keys
}

// Repos returns every configured repository once, sorted by name.
func (c *Config) Repos() []Repo {
	byName := map[string]Repo{}
	for _, s := range c.Spaces {
		for _, r := range s.Repos {
			if _, ok := byName[r.Name]; !ok {
				byName[r.Name] = r
			}
		}
	}
	out := make([]Repo, 0, len(byName))
	for _, r := range byName {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r Repo) Owner() string {
	owner, _, _ := strings.Cut(r.Name, "/")
	return owner
}

func (r Repo) ShortName() string {
	_, name, _ := strings.Cut(r.Name, "/")
	return name
}

// Clone returns a deep copy, so an editor can change it without touching the config in use.
func (c Config) Clone() Config {
	out := c
	out.RepoRoots = slices.Clone(c.RepoRoots)
	out.GitHub.Accounts = maps.Clone(c.GitHub.Accounts)
	out.Spaces = make([]Space, len(c.Spaces))
	for i, s := range c.Spaces {
		s.StatusOverrides = maps.Clone(s.StatusOverrides)
		s.Repos = slices.Clone(s.Repos)
		s.Ignored = slices.Clone(s.Ignored)
		out.Spaces[i] = s
	}
	return out
}

// GitHubAccount returns the gh account configured for owner; empty means gh's active account.
func (c *Config) GitHubAccount(owner string) string {
	return c.GitHub.Accounts[owner]
}

// Categories returns the board column for each overridden status of a space.
func (s Space) Categories() map[string]ticket.Category {
	out := make(map[string]ticket.Category, len(s.StatusOverrides))
	for status, cat := range s.StatusOverrides {
		if c, ok := ticket.ParseCategory(cat); ok {
			out[status] = c
		}
	}
	return out
}
