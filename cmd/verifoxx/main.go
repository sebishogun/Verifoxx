// Command verifoxx is the entry point for the Verifoxx policy engine.
package main

import (
	"os"

	"github.com/sebishogun/verifoxx/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
