package credstore

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

var ErrNotFound = errors.New("credential not found")

const service = "tkt"

// EnvJiraToken overrides the stored Jira token; documented as the less secure fallback for tests and headless use.
const EnvJiraToken = "TKT_JIRA_TOKEN"

type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
	// Name describes where credentials live, for messages such as "saved in macOS Keychain".
	Name() string
}

// JiraKey identifies the Jira token of one account on one site.
func JiraKey(site, email string) string {
	host := site
	if u, err := url.Parse(site); err == nil && u.Host != "" {
		host = u.Host
	}
	return "jira:" + host + ":" + strings.ToLower(email)
}

type Source string

const (
	SourceEnv   Source = "environment"
	SourceStore Source = "store"
)

// JiraToken returns the Jira token, preferring TKT_JIRA_TOKEN when it is set.
func JiraToken(s Store, getenv func(string) string, site, email string) (string, Source, error) {
	if v := strings.TrimSpace(getenv(EnvJiraToken)); v != "" {
		return v, SourceEnv, nil
	}
	v, err := s.Get(JiraKey(site, email))
	if err != nil {
		return "", "", err
	}
	return v, SourceStore, nil
}

// IsWSL reports whether the process runs inside Windows Subsystem for Linux.
func IsWSL(getenv func(string) string, readFile func(string) ([]byte, error)) bool {
	if getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := readFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// Default picks the platform store: Windows Credential Manager under WSL, the OS keyring elsewhere.
func Default(goos string) Store {
	if goos == "linux" && IsWSL(os.Getenv, os.ReadFile) {
		return NewWSL(nil)
	}
	return Keyring{GOOS: goos}
}
