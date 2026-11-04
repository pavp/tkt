package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

func newAPICmd(a *App) *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "api METHOD PATH",
		Short: "Send one request to the tkt daemon and print its answer as JSON",
		Long: "Sends one request to the running tkt daemon (starting it when none runs), with its token, and prints\n" +
			"{\"status\": <code>, \"body\": <the answer>}. The Herdr desktop mod uses it to reach the daemon of another\n" +
			"machine of the core. PATH is a daemon route such as /v1/board; --body sends JSON (\"-\" reads stdin).\n" +
			"Writes run at once, as the daemon API does: the client confirms with the person first.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method, path := strings.ToUpper(args[0]), args[1]
			if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, method) {
				return fmt.Errorf("%s is not a method the daemon answers: use GET, POST, PUT, PATCH or DELETE", args[0])
			}
			if !strings.HasPrefix(path, "/v1/") && path != "/health" {
				return fmt.Errorf("%s is not a daemon route: give a path such as /v1/board", path)
			}
			var sent []byte
			switch {
			case body == "-":
				b, err := io.ReadAll(a.Stdin)
				if err != nil {
					return err
				}
				sent = b
			case body != "":
				sent = []byte(body)
			}
			if sent != nil && !json.Valid(sent) {
				return errors.New("--body is not JSON")
			}
			c, err := a.daemonClient(cmd.Context())
			if err != nil {
				return fmt.Errorf("the tkt daemon is not reachable: %w", err)
			}
			status, out, err := c.Call(cmd.Context(), method, path, sent)
			if err != nil {
				return fmt.Errorf("the tkt daemon did not answer: %w", err)
			}
			answer := json.RawMessage(out)
			if !json.Valid(out) {
				answer, _ = json.Marshal(string(out))
			}
			b, err := json.Marshal(struct {
				Status int             `json:"status"`
				Body   json.RawMessage `json:"body"`
			}{status, answer})
			if err != nil {
				return err
			}
			a.printf("%s\n", b)
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "the request's JSON body (\"-\" reads it from stdin)")
	return cmd
}
