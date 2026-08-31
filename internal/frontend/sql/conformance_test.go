package sql

import (
	"slices"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func TestPostgreSQLRLSCommandRoleAndNullConformance(t *testing.T) {
	source := []byte(`
CREATE POLICY select_team ON records FOR SELECT TO analyst USING (team = 'blue');
CREATE POLICY insert_count ON records FOR INSERT TO PUBLIC WITH CHECK (count < 10);
CREATE POLICY update_row ON records FOR UPDATE TO analyst USING (team = 'blue') WITH CHECK (count < 10);
CREATE POLICY delete_team ON records FOR DELETE TO analyst USING (team = 'blue');
CREATE POLICY verified ON records AS RESTRICTIVE FOR ALL TO PUBLIC USING (enabled) WITH CHECK (enabled);
`)
	var compiler Compiler
	var compiled program.Program
	diagnostics, err := compiler.CompileRLS(&compiled, source, testRLSSchema(t), public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("CompileRLS() = diagnostics %#v error %v", diagnostics, err)
	}
	blue, red := "blue", "red"
	analyst, outsider := "analyst", "outsider"
	selectCommand, insertCommand := "select", "insert"
	updateUsing, updateCheck, deleteCommand := "update_using", "update_check", "delete"
	unknownCommand := "truncate"
	count5, count11 := int64(5), int64(11)
	yes, no := true, false
	tests := []struct {
		name       string
		activation rlsActivation
		outcome    schema.OutcomeID
		missing    bool
	}{
		{name: "select permits", activation: rlsActivation{team: &blue, enabled: &yes, command: &selectCommand, role: &analyst}, outcome: 1},
		{name: "select predicate denies", activation: rlsActivation{team: &red, enabled: &yes, command: &selectCommand, role: &analyst}, outcome: 2},
		{name: "select role denies", activation: rlsActivation{team: &blue, enabled: &yes, command: &selectCommand, role: &outsider}, outcome: 2},
		{name: "select restriction denies", activation: rlsActivation{team: &blue, enabled: &no, command: &selectCommand, role: &analyst}, outcome: 2},
		{name: "select null denies with missing", activation: rlsActivation{enabled: &yes, command: &selectCommand, role: &analyst}, outcome: 2, missing: true},
		{name: "insert permits", activation: rlsActivation{count: &count5, enabled: &yes, command: &insertCommand, role: &outsider}, outcome: 1},
		{name: "insert check denies", activation: rlsActivation{count: &count11, enabled: &yes, command: &insertCommand, role: &outsider}, outcome: 2},
		{name: "update using permits", activation: rlsActivation{team: &blue, enabled: &yes, command: &updateUsing, role: &analyst}, outcome: 1},
		{name: "update check permits", activation: rlsActivation{count: &count5, enabled: &yes, command: &updateCheck, role: &analyst}, outcome: 1},
		{name: "delete permits", activation: rlsActivation{team: &blue, enabled: &yes, command: &deleteCommand, role: &analyst}, outcome: 1},
		{name: "missing command denies", activation: rlsActivation{team: &blue, enabled: &yes, role: &analyst}, outcome: 2, missing: true},
		{name: "unsupported runtime command denies", activation: rlsActivation{team: &blue, enabled: &yes, command: &unknownCommand, role: &analyst}, outcome: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome, reasons := evaluateRLS(t, &compiled, test.activation)
			if outcome != test.outcome {
				t.Fatalf("outcome = %d, want %d (reasons %v)", outcome, test.outcome, reasons)
			}
			if test.missing && !slices.Contains(reasons, truth.ReasonMissing) {
				t.Fatalf("reasons = %v, want missing", reasons)
			}
		})
	}
}

func TestPostgreSQLRLSCombinesMultiplePermissiveAndRestrictivePolicies(t *testing.T) {
	source := []byte(`
CREATE POLICY blue ON records FOR SELECT TO analyst USING (team = 'blue');
CREATE POLICY small ON records FOR SELECT TO analyst USING (count < 10);
CREATE POLICY enabled ON records AS RESTRICTIVE FOR ALL TO PUBLIC USING (enabled);
CREATE POLICY known_team ON records AS RESTRICTIVE FOR SELECT TO PUBLIC USING (team IN ('blue', 'green'));
`)
	var compiler Compiler
	var compiled program.Program
	diagnostics, err := compiler.CompileRLS(&compiled, source, testRLSSchema(t), public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("CompileRLS() = diagnostics %#v error %v", diagnostics, err)
	}
	blue, green, red := "blue", "green", "red"
	analyst, outsider := "analyst", "outsider"
	command := "select"
	count5, count20 := int64(5), int64(20)
	yes, no := true, false
	tests := []struct {
		name       string
		activation rlsActivation
		want       schema.OutcomeID
	}{
		{name: "first permissive", activation: rlsActivation{team: &blue, count: &count20, enabled: &yes, command: &command, role: &analyst}, want: 1},
		{name: "second permissive", activation: rlsActivation{team: &green, count: &count5, enabled: &yes, command: &command, role: &analyst}, want: 1},
		{name: "second restrictive denies", activation: rlsActivation{team: &red, count: &count5, enabled: &yes, command: &command, role: &analyst}, want: 2},
		{name: "first restrictive denies", activation: rlsActivation{team: &blue, count: &count5, enabled: &no, command: &command, role: &analyst}, want: 2},
		{name: "no role permissive denies", activation: rlsActivation{team: &blue, count: &count5, enabled: &yes, command: &command, role: &outsider}, want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := evaluateRLS(t, &compiled, test.activation)
			if got != test.want {
				t.Fatalf("outcome = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPostgreSQLRLSOmittedClausesApplyToEveryCommand(t *testing.T) {
	var compiler Compiler
	var compiled program.Program
	diagnostics, err := compiler.CompileRLS(&compiled, []byte(`CREATE POLICY defaults ON records TO analyst;`), testRLSSchema(t), public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("CompileRLS() = diagnostics %#v error %v", diagnostics, err)
	}
	analyst, outsider := "analyst", "outsider"
	for _, command := range []string{"select", "insert", "update_using", "update_check", "delete"} {
		t.Run(command, func(t *testing.T) {
			got, _ := evaluateRLS(t, &compiled, rlsActivation{command: &command, role: &analyst})
			if got != 1 {
				t.Fatalf("analyst outcome = %d, want approve", got)
			}
			got, _ = evaluateRLS(t, &compiled, rlsActivation{command: &command, role: &outsider})
			if got != 2 {
				t.Fatalf("outsider outcome = %d, want reject", got)
			}
		})
	}
}

type rlsActivation struct {
	team    *string
	count   *int64
	enabled *bool
	command *string
	role    *string
}

func evaluateRLS(t *testing.T, compiled *program.Program, activation rlsActivation) (schema.OutcomeID, []schema.ReasonID) {
	t.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	setSymbol := func(field schema.FieldID, value *string) {
		if value == nil {
			return
		}
		symbol, err := builder.InternSymbol([]byte(*value))
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, field, symbol); err != nil {
			t.Fatal(err)
		}
	}
	setSymbol(1, activation.team)
	if activation.count != nil {
		if err := builder.SetInteger(0, 2, *activation.count); err != nil {
			t.Fatal(err)
		}
	}
	if activation.enabled != nil {
		if err := builder.SetBoolean(0, 3, *activation.enabled); err != nil {
			t.Fatal(err)
		}
	}
	setSymbol(4, activation.command)
	setSymbol(5, activation.role)
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var outcomes result.Batch
	if err := executor.Execute(&outcomes, compiled, batch); err != nil {
		t.Fatal(err)
	}
	start, end := outcomes.ReasonOffsets[0], outcomes.ReasonOffsets[1]
	return outcomes.OutcomeIDs[0], append([]schema.ReasonID(nil), outcomes.ReasonIDs[start:end]...)
}
