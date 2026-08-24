package cmd

import "time"

func debugCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "debug":
		return []commandSpec{{
			executable: "dlv",
			arguments: []string{
				"debug", "./cmd/verifoxx", "--headless", "--listen", "127.0.0.1:38697", "--api-version=2",
				"--accept-multiclient", "--build-flags=-tags=debug -gcflags=all=-N -l", "--",
				"debug-worker", "--socket", ".verifoxx/debug.sock",
			},
			repositoryPathArguments: []uint8{11},
			timeout:                 30 * time.Minute,
		}}, true
	case "debug:dap":
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
