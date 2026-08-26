package cel

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	internalfrontend "github.com/sebishogun/nornrune/internal/frontend"
)

func FuzzCompile(f *testing.F) {
	f.Add([]byte(`team == "blue"`))
	f.Add([]byte(`request.team in ["blue", "green"]`))
	f.Add([]byte{0xff, 0xfe})
	bindings := scalarBindings()
	limits := public.DefaultLimits()
	limits.MaxSourceBytes = 4096
	f.Fuzz(func(t *testing.T, source []byte) {
		policy, diagnostics := Compile(source, bindings, limits)
		if policy == nil {
			if len(diagnostics) == 0 {
				t.Fatal("nil policy without diagnostics")
			}
			if uint64(len(diagnostics)) > uint64(limits.MaxDiagnostics) {
				t.Fatalf("diagnostics = %d, limit %d", len(diagnostics), limits.MaxDiagnostics)
			}
			return
		}
		if len(diagnostics) != 0 {
			t.Fatalf("policy returned with diagnostics: %+v", diagnostics)
		}
		compiled, semanticDiagnostics, err := internalfrontend.Compile(policy)
		if err != nil || len(semanticDiagnostics) != 0 || compiled == nil {
			t.Fatalf("invalid semantic policy: compiled=%v diagnostics=%+v error=%v", compiled, semanticDiagnostics, err)
		}
	})
}
