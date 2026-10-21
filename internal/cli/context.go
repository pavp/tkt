package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/jira"
)

// maxImageBytes bounds each downloaded image; anything larger stays a link.
const maxImageBytes = 20 << 20

func newContextCmd(a *App) *cobra.Command {
	var imagesDir string
	cmd := &cobra.Command{
		Use:   "context [KEY]",
		Short: "Print the ticket as Markdown for an agent (save it as .ticket.md)",
		Long: "Print the ticket as Markdown: title, description, acceptance criteria, attachments, the latest\n" +
			"comments and its pull requests. Agents save it as .ticket.md in their worktree:\n\n" +
			"  tkt context DEMO-123 --images .ticket-assets > .ticket.md\n\n" +
			"--images downloads image attachments into the directory so the agent can look at them; other\n" +
			"attachments are linked. Without KEY, the ticket is taken from the current git branch.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.LoadConfig()
			if err != nil {
				return err
			}
			key, err := a.resolveKey(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			d, err := a.ticketDetail(cmd.Context(), c, key)
			if err != nil {
				return err
			}
			local := map[string]string{}
			if imagesDir != "" {
				js, err := a.jiraClient(c)
				if err != nil {
					return err
				}
				local = a.downloadImages(cmd, js, d.Attachments, imagesDir)
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"detail": d, "images": local})
			}
			fmt.Fprint(a.Stdout, renderContext(d, local))
			return nil
		},
	}
	cmd.Flags().StringVar(&imagesDir, "images", "", "download image attachments into this directory")
	return cmd
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// downloadImages saves image attachments and returns filename → local path; failures become warnings on stderr.
func (a *App) downloadImages(cmd *cobra.Command, js JiraAPI, atts []jira.Attachment, dir string) map[string]string {
	out := map[string]string{}
	used := map[string]bool{}
	for _, at := range atts {
		if !strings.HasPrefix(at.MimeType, "image/") {
			continue
		}
		if at.Size > maxImageBytes {
			fmt.Fprintf(a.Stderr, "! %s is larger than %s; linked instead\n", at.Filename, humanSize(maxImageBytes))
			continue
		}
		name := unsafeName.ReplaceAllString(filepath.Base(at.Filename), "_")
		if name == "" || name == "." || name == ".." {
			name = "attachment"
		}
		for base, i := name, 2; used[name]; i++ {
			ext := filepath.Ext(base)
			name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), i, ext)
		}
		used[name] = true
		var buf bytes.Buffer
		if err := js.Download(cmd.Context(), at.Content, &buf, maxImageBytes); err != nil {
			fmt.Fprintf(a.Stderr, "! %s not downloaded: %v\n", at.Filename, err)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(a.Stderr, "! images not downloaded: %v\n", err)
			return out
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			fmt.Fprintf(a.Stderr, "! %s not saved: %v\n", at.Filename, err)
			continue
		}
		out[at.Filename] = path
	}
	return out
}

func renderContext(d detail, images map[string]string) string {
	var b strings.Builder
	t := d.Ticket
	fmt.Fprintf(&b, "# %s · %s\n\n", t.Key, t.Summary)
	fmt.Fprintf(&b, "- Type: %s\n- Status: %s\n- Space: %s\n- Jira: %s\n", t.Type, t.Status, t.Space(), t.URL)
	if t.Parent != "" {
		fmt.Fprintf(&b, "- Parent: %s\n", t.Parent)
	}
	fmt.Fprintf(&b, "\n## Description\n\n%s\n", orDefault(d.Description, "_No description._"))
	if d.Acceptance != "" {
		fmt.Fprintf(&b, "\n## Acceptance criteria\n\n%s\n", d.Acceptance)
	}
	if len(t.PRs) > 0 {
		b.WriteString("\n## Pull requests\n\n")
		for _, pr := range t.PRs {
			fmt.Fprintf(&b, "- [%s#%d](%s) · %s%s\n", pr.Repo, pr.Number, pr.URL, prStateText(pr), reviewText(pr))
		}
	}
	if len(d.Attachments) > 0 {
		b.WriteString("\n## Attachments\n\n")
		for _, at := range d.Attachments {
			if p, ok := images[at.Filename]; ok {
				fmt.Fprintf(&b, "- ![%s](%s)\n", at.Filename, filepath.ToSlash(p))
			} else {
				fmt.Fprintf(&b, "- [%s](%s) (%s)\n", at.Filename, at.Content, humanSize(at.Size))
			}
		}
	}
	if len(d.Comments) > 0 {
		b.WriteString("\n## Latest comments\n")
		for _, cm := range d.Comments {
			fmt.Fprintf(&b, "\n### %s · %s\n\n%s\n", cm.Author, cm.Created.UTC().Format("2006-01-02"), cm.Body)
		}
	}
	return b.String()
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
