// Package herdrplugin renders the Herdr plugin manifest that points Herdr at this tkt binary.
package herdrplugin

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed manifest.toml.tmpl
var manifest string

// ID is the plugin id Herdr uses for actions such as "tkt.board".
const ID = "tkt"

// FileName is the manifest name Herdr looks for in a plugin directory.
const FileName = "herdr-plugin.toml"

// Render fills the manifest with the absolute path of the tkt binary, so Herdr never depends on PATH.
func Render(tktPath, version string) string {
	return strings.NewReplacer("{{TKT}}", tomlString(tktPath), "{{VERSION}}", tomlString(version)).Replace(manifest)
}

// tomlString quotes s as a TOML basic string; Go's strconv.Quote is close but emits \x escapes TOML rejects.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
