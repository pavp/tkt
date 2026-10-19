package repos

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/execx"
)

var ErrNotFound = errors.New("local clone not found")

// maxDepth bounds the scan below each root; deeper trees are better configured with an explicit path.
const maxDepth = 3

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, ".cache": true}

type Resolver struct {
	Roots []string
	Run   execx.Runner
}

// Resolve returns the local checkout for repo: its configured path, or a clone under Roots whose
// origin remote is the same GitHub repository.
func (r Resolver) Resolve(ctx context.Context, repo config.Repo) (string, error) {
	if repo.Path != "" {
		fi, err := os.Stat(repo.Path)
		if err != nil || !fi.IsDir() {
			return "", fmt.Errorf("%s: configured path %s is not a directory", repo.Name, repo.Path)
		}
		return repo.Path, nil
	}
	for _, root := range r.Roots {
		if found := r.scan(ctx, root, repo.Name, 1); len(found) > 0 {
			return found[0], nil
		}
	}
	return "", fmt.Errorf("%s: %w in %s", repo.Name, ErrNotFound, describeRoots(r.Roots))
}

// FindAll lists every clone of repo under Roots, in walk order.
func (r Resolver) FindAll(ctx context.Context, repo string) []string {
	var out []string
	for _, root := range r.Roots {
		out = append(out, r.scan(ctx, root, repo, 0)...)
	}
	return out
}

// RepoAt returns the GitHub repository ("owner/name") that dir's origin remote points to.
func (r Resolver) RepoAt(ctx context.Context, dir string) (string, bool) {
	out, err := r.Run.Run(ctx, dir, "git", []string{"remote", "get-url", "origin"}, nil)
	if err != nil {
		return "", false
	}
	owner, name, ok := ParseRemote(strings.TrimSpace(string(out)))
	if !ok {
		return "", false
	}
	return owner + "/" + name, true
}

func describeRoots(roots []string) string {
	if len(roots) == 0 {
		return "any root (set repo_roots in the config)"
	}
	return strings.Join(roots, ", ")
}

// scan walks root for clones of want, stopping after limit matches (0 means all).
func (r Resolver) scan(ctx context.Context, root, want string, limit int) []string {
	var found []string
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			if r.matches(ctx, path, want) {
				found = append(found, path)
				if limit > 0 && len(found) >= limit {
					return filepath.SkipAll
				}
			}
			return filepath.SkipDir
		}
		if strings.Count(path, string(filepath.Separator))-rootDepth >= maxDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

func (r Resolver) matches(ctx context.Context, dir, want string) bool {
	got, ok := r.RepoAt(ctx, dir)
	return ok && strings.EqualFold(got, want)
}

// Clone clones repo into the first root with gh and returns the new checkout.
func (r Resolver) Clone(ctx context.Context, repo config.Repo) (string, error) {
	if len(r.Roots) == 0 {
		return "", errors.New("set repo_roots in the config so tkt knows where to clone")
	}
	dest := filepath.Join(r.Roots[0], repo.ShortName())
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists and is not a clone of %s", dest, repo.Name)
	}
	if err := os.MkdirAll(r.Roots[0], 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", r.Roots[0], err)
	}
	if _, err := r.Run.Run(ctx, "", "gh", []string{"repo", "clone", repo.Name, dest}, nil); err != nil {
		return "", fmt.Errorf("clone %s: %w", repo.Name, err)
	}
	return dest, nil
}

// ParseRemote extracts owner and name from a GitHub remote URL in SSH, scp-like or HTTPS form.
func ParseRemote(remote string) (owner, name string, ok bool) {
	s := strings.TrimSuffix(strings.TrimSpace(remote), "/")
	s = strings.TrimSuffix(s, ".git")
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	case strings.Contains(s, "github.com/"):
		s = s[strings.Index(s, "github.com/")+len("github.com/"):]
	default:
		return "", "", false
	}
	owner, name, found := strings.Cut(s, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}
