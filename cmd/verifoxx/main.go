// Command verifoxx is the entry point for the Verifoxx policy engine.
package main

import (
	"os"

	"github.com/sebishogun/verifoxx/internal/app"
)

func main() {
	os.Exit(app.RunWithInput(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
