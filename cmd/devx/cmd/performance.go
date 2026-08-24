package cmd

import "time"

func performanceCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "bench":
		return []commandSpec{{
			executable: "go",
			arguments: []string{
				"test", "-run", "^$", "-bench", "^BenchmarkEvaluate$", "-benchmem",
				"-benchtime", "200ms", "-count", "6", "-timeout", "120s", "./internal/eval",
			},
			timeout: 3 * time.Minute,
		}}, true
	case "bench:compare":
		return []commandSpec{{
			executable: "sh", arguments: []string{"scripts/bench-compare.sh"}, timeout: 15 * time.Minute,
		}}, true
	case "profile":
		return []commandSpec{{
			executable: "go",
			arguments: []string{
				"test", "-run", "^$", "-bench", "^BenchmarkDemoPipeline$", "-benchtime", "5s",
				"-cpuprofile", "cpu.pprof", "-timeout", "60s", "./internal/adapters/cli",
			},
			timeout: 90 * time.Second,
		}}, true
	case "perf":
		return []commandSpec{{
			executable: "perf",
			arguments: []string{
				"stat", "--", "go", "test", "-run", "^$", "-bench", "^BenchmarkDemoPipeline$", "-benchtime", "5s", "./internal/adapters/cli",
			},
			timeout: 90 * time.Second,
		}}, true
	case "load":
		return []commandSpec{{
			executable: "go",
			arguments: []string{
				"run", "./cmd/loadgen", "-protocol", "http", "-target", "127.0.0.1:8080",
				"-requests", "1000", "-concurrency", "4", "-timeout", "30s",
			},
			timeout: 2 * time.Minute,
		}}, true
	default:
		return nil, false
	}
}
