// Command devx runs NornRune developer workflows.
package main

import (
	"fmt"
	"os"

	devx "github.com/sebishogun/nornrune/cmd/devx/cmd"
)

func main() {
	if err := devx.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
