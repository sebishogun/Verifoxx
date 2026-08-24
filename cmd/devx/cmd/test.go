package cmd

import "time"

func testCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "test":
		return []commandSpec{{
			executable: "go", arguments: []string{"test", "-count=1", "-timeout", "60s", "./..."}, timeout: 2 * time.Minute,
		}}, true
	case "test:unit":
		return []commandSpec{{
			executable: "go", arguments: []string{"test", "-count=1", "-timeout", "60s", "./internal/...", "./cmd/..."}, timeout: 2 * time.Minute,
		}}, true
	case "test:integration":
		return []commandSpec{{
			executable: "go", arguments: []string{"test", "-count=1", "-tags=integration", "-timeout", "300s", "./..."}, timeout: 6 * time.Minute,
		}}, true
	case "test:e2e":
		return []commandSpec{{
			executable: "go", arguments: []string{"test", "-count=1", "-tags=docker", "-timeout", "600s", "./internal/e2e"}, timeout: 11 * time.Minute,
		}}, true
	case "test:race":
		return []commandSpec{{
			executable: "go", arguments: []string{"test", "-count=1", "-race", "-timeout", "180s", "./..."}, timeout: 4 * time.Minute,
		}}, true
	case "fuzz":
		return []commandSpec{
			{
				executable: "go",
				arguments:  []string{"test", "-run", "^$", "-fuzz", "^FuzzDecodeBatch$", "-fuzztime", "30s", "-timeout", "60s", "./internal/adapters/jsonbatch"},
				timeout:    90 * time.Second,
			},
			{
				executable: "go",
				arguments:  []string{"test", "-run", "^$", "-fuzz", "^FuzzDecode$", "-fuzztime", "30s", "-timeout", "60s", "./internal/adapters/jsonpolicy"},
				timeout:    90 * time.Second,
			},
		}, true
	default:
		return nil, false
	}
}
