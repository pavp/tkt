package main

import (
	"os"

	"github.com/pavp/tkt/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
