// Package nornrune provides the embedded semantic policy and typed field
// schema for the NornRune baseline policy pack.
package nornrune

import (
	_ "embed"

	"github.com/sebishogun/nornrune/internal/schema"
)

//go:embed policy.json
var source string

var fieldSpecs = [...]struct {
	name  string
	group schema.FieldGroup
}{
	{"requester.team", schema.FieldGroupSubject},
	{"requester.trust", schema.FieldGroupSubject},
	{"action.type", schema.FieldGroupAction},
	{"action.output", schema.FieldGroupOutput},
	{"action.dataset", schema.FieldGroupResource},
	{"environment.execution_env", schema.FieldGroupContext},
	{"environment.usage", schema.FieldGroupContext},
}

// Source returns the immutable embedded semantic policy document.
func Source() string { return source }

// NewSchema constructs the field schema and name interner for this policy
// pack. Callers own both returned values.
func NewSchema() (*schema.Schema, *schema.Interner, error) {
	symbols := schema.NewSymbolInterner(16)
	fields := schema.NewBuilder()
	for _, spec := range fieldSpecs {
		name, err := symbols.Intern([]byte(spec.name))
		if err != nil {
			return nil, nil, err
		}
		if _, err := fields.AddField(name, schema.ValueKindSymbol, spec.group); err != nil {
			return nil, nil, err
		}
	}
	return fields.Finish(), symbols, nil
}
