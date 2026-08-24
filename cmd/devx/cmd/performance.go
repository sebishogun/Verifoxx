package cmd

import "time"

func performanceCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "bench":
		return []commandSpec{{
			executable: "go",
			arguments:  []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-timeout", "120s", "./..."},
			timeout:    3 * time.Minute,
		}}, true
	case "bench:compare":
		return []commandSpec{{
			executable: "benchstat", arguments: []string{"bench/baseline.txt", "bench/current.txt"}, timeout: time.Minute,
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
			executable: "ghz",
			arguments: []string{
				"--insecure", "--proto", "api/proto/verifoxx/v1/verifoxx.proto",
				"--call", "verifoxx.v1.PolicyService/EvaluateBatch", "127.0.0.1:9090",
			},
			timeout: 5 * time.Minute,
		}}, true
	default:
		return nil, false
	}
}
