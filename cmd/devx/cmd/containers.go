package cmd

import "time"

func containerCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "docker:build":
		return []commandSpec{{
			executable: "docker", arguments: []string{"build", "-t", "verifoxx:dev", "."}, timeout: 10 * time.Minute,
		}}, true
	case "docker:run":
		return []commandSpec{{
			executable: "docker", arguments: []string{"run", "--rm", "verifoxx:dev", "demo"}, timeout: 2 * time.Minute,
		}}, true
	case "docker:full":
		return []commandSpec{{
			executable: "docker", arguments: []string{"compose", "--profile", "full", "up", "--build", "--wait"}, timeout: 10 * time.Minute,
		}}, true
	default:
		return nil, false
	}
}
