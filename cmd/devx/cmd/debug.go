package cmd

import "time"

func debugCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "debug", "debug:dap":
		return []commandSpec{{
			executable: "dlv", arguments: []string{"dap", "--listen", "127.0.0.1:38697", "--log"}, timeout: 30 * time.Minute,
		}}, true
	case "debug:tui":
		return []commandSpec{{
			executable:              "go",
			arguments:               []string{"run", "-tags=debug", "./cmd/verifoxx", "tui", "--socket", ".verifoxx/debug.sock"},
			repositoryPathArguments: []uint8{5},
			timeout:                 30 * time.Minute,
		}}, true
	default:
		return nil, false
	}
}
