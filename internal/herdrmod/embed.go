// Package herdrmod carries the Herdr desktop mod that `tkt herdr install` puts into Herdr desktop's app folder.
package herdrmod

import (
	"embed"
	"io/fs"
)

//go:embed mod
var mod embed.FS

// Files is the mod's folder: main.js, mod.json and its lib.
func Files() fs.FS {
	sub, err := fs.Sub(mod, "mod")
	if err != nil {
		panic(err)
	}
	return sub
}
