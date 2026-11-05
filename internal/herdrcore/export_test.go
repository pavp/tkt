package herdrcore

import "time"

// SetQuestionAfter shortens how long StartAgent waits for a first status before calling the agent not ready.
func SetQuestionAfter(c *Client, d time.Duration) { c.questionAfter = d }

// SetNewID makes the space and tab ids a new space gets predictable.
func SetNewID(c *Client, f func(prefix string) string) { c.newID = f }

// SetSettle sets how often, and how far apart, window actions on a space or pane the window may not show yet are tried.
func SetSettle(c *Client, d time.Duration, tries int) { c.settle, c.settleTries = d, tries }

// SetLookPath sets how the client finds an agent's program on tkt's own PATH.
func SetLookPath(c *Client, f func(string) (string, error)) { c.lookPath = f }

// Program is the program the client runs for an agent kind.
func Program(c *Client, kind string) string { return c.program(kind) }

// SetEnviron sets the environment the client hands, in part, to the terminals it starts.
func SetEnviron(c *Client, f func() []string) { c.environ = f }

// Argv is the command line the client starts for an agent kind, before a Start's own arguments.
func Argv(c *Client, kind string) []string { return c.argvOf(kind) }

// Env is what the terminals the client starts take from tkt.
func Env(c *Client) map[string]string { return c.env() }
