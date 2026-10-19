package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const authStatus = `github.com
  ✓ Logged in to github.com account personal-dev (keyring)
  - Active account: true
  - Git operations protocol: ssh
  ✓ Logged in to github.com account work-dev (keyring)
  - Active account: false
`

func TestLoggedInAccountsActiveFirst(t *testing.T) {
	r := &ghRunner{out: authStatus}
	if got := LoggedInAccounts(context.Background(), r); !reflect.DeepEqual(got, []string{"personal-dev", "work-dev"}) {
		t.Errorf("got %v", got)
	}
	if r.args[1] != "auth" || r.args[2] != "status" {
		t.Errorf("ran %v", r.args)
	}
	swapped := "  ✓ Logged in to github.com account a (keyring)\n  - Active account: false\n  ✓ Logged in to github.com account b (keyring)\n  - Active account: true\n"
	if got := LoggedInAccounts(context.Background(), &ghRunner{out: swapped, err: errors.New("exit status 1")}); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("active account should come first even when gh exits non-zero: %v", got)
	}
}

func TestOrgMember(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/memberships/orgs/example-org" {
			http.NotFound(w, r)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer member":
			_, _ = w.Write([]byte(`{"state":"active"}`))
		case "Bearer pending":
			_, _ = w.Write([]byte(`{"state":"pending"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(nil).WithEndpoints(srv.URL, srv.Client())
	for tok, want := range map[string]bool{"member": true, "pending": false, "stranger": false} {
		got, err := c.OrgMember(context.Background(), tok, "example-org")
		if err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", tok, got, err, want)
		}
	}
}

func TestDetectAccount(t *testing.T) {
	tokens := map[string]string{"personal-dev": "t-personal", "work-dev": "t-work"}
	tokenFor := func(_ context.Context, a string) (string, error) { return tokens[a], nil }
	member := func(_ context.Context, tok, org string) (bool, error) {
		return tok == "t-work" && org == "example-org", nil
	}
	accounts := []string{"personal-dev", "work-dev"}
	tests := []struct{ owner, want string }{
		{"example-org", "work-dev"},
		{"Personal-Dev", "personal-dev"},
		{"other-org", ""},
	}
	for _, tt := range tests {
		if got := DetectAccount(context.Background(), accounts, tt.owner, tokenFor, member); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.owner, got, tt.want)
		}
	}
}
