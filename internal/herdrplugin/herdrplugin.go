// Package herdrplugin renders the Herdr plugin manifest that points Herdr at this tkt binary.
package herdrplugin

import (
	_ "embed"
	"strconv"
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
	return strings.NewReplacer("{{TKT}}", strconv.Quote(tktPath), "{{VERSION}}", strconv.Quote(version)).Replace(manifest)
}
