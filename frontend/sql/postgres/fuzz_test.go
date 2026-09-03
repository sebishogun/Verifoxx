package postgres

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
)

func FuzzRLS(f *testing.F) {
	f.Add([]byte(`CREATE POLICY p ON records TO PUBLIC USING (enabled);`))
	f.Add([]byte(`CREATE POLICY p ON records AS RESTRICTIVE FOR UPDATE TO analyst WITH CHECK (count < 10);`))
	f.Fuzz(func(t *testing.T, source []byte) {
		rls, diagnostics := CompileRLS(source, rlsSchema(t), public.DefaultLimits())
		if rls == nil {
			if len(diagnostics) == 0 {
				t.Fatal("CompileRLS() returned neither result nor diagnostics")
			}
			return
		}
		if len(diagnostics) != 0 || rls.Semantic == nil {
			t.Fatalf("CompileRLS() returned result and diagnostics %#v", diagnostics)
		}
		rows := len(rls.Modes)
		if len(rls.Commands) != rows || len(rls.UsingRoots) != rows || len(rls.CheckRoots) != rows || len(rls.RoleStarts) != rows || len(rls.RoleCounts) != rows {
			t.Fatal("RLS columns differ")
		}
	})
}
