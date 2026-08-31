package sql

import (
	"unicode/utf8"

	public "github.com/sebishogun/nornrune/frontend"
)

// Schema owns the explicit field and compile-time parameter declarations used
// by one SQL source.
type Schema struct {
	Bindings     public.BindingSet
	CommandField string
	RoleField    string
	Parameters   []Parameter
	Dialect      Dialect
}

// NewSchema validates and owns one SQL declaration environment.
func NewSchema(
	dialect Dialect,
	bindings public.BindingSet,
	parameters []Parameter,
	commandField, roleField string,
	limits public.Limits,
) (Schema, error) {
	schema := Schema{
		Bindings:     cloneBindingSet(bindings),
		Parameters:   cloneParameters(parameters),
		CommandField: commandField,
		RoleField:    roleField,
		Dialect:      dialect,
	}
	if err := schema.Validate(limits); err != nil {
		return Schema{}, err
	}
	return schema, nil
}

// Validate checks schema without mutating it.
func (schema Schema) Validate(limits public.Limits) error {
	if !schema.Dialect.Valid() || schema.Bindings.Validate(limits) != nil {
		return ErrInvalidSchema
	}
	if schema.CommandField != "" && schema.CommandField == schema.RoleField {
		return ErrInvalidSchema
	}
	if !validSpecialField(schema.Bindings, schema.CommandField) || !validSpecialField(schema.Bindings, schema.RoleField) {
		return ErrInvalidSchema
	}

	stringBytes := uint64(len(schema.Bindings.Name) + len(schema.Bindings.Version) + len(schema.Bindings.Decision))
	for row := range schema.Bindings.Fields {
		stringBytes += uint64(len(schema.Bindings.Fields[row].Source) + len(schema.Bindings.Fields[row].Target))
	}
	stringBytes += uint64(len(schema.CommandField) + len(schema.RoleField))
	for row := range schema.Parameters {
		parameter := &schema.Parameters[row]
		if !validParameterName(schema.Dialect, parameter.Name) || !validParameterLiteral(parameter.Value) {
			return ErrInvalidSchema
		}
		stringBytes += uint64(len(parameter.Name) + len(parameter.Value.String))
		for previous := 0; previous < row; previous++ {
			if schema.Parameters[previous].Name == parameter.Name {
				return ErrInvalidSchema
			}
		}
	}
	if stringBytes > uint64(limits.MaxStringBytes) {
		return ErrInvalidSchema
	}
	return nil
}

func validSpecialField(bindings public.BindingSet, name string) bool {
	if name == "" {
		return true
	}
	for row := range bindings.Fields {
		binding := &bindings.Fields[row]
		if binding.Source == name {
			return binding.Kind == public.ValueKindString
		}
	}
	return false
}

func validParameterName(dialect Dialect, name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	switch dialect {
	case DialectPostgreSQL:
		if len(name) < 2 || name[0] != '$' || name[1] < '1' || name[1] > '9' {
			return false
		}
		for row := 2; row < len(name); row++ {
			if name[row] < '0' || name[row] > '9' {
				return false
			}
		}
		return true
	case DialectSnowflake, DialectDatabricks:
		if name == "?" {
			return true
		}
		if len(name) < 2 || name[0] != ':' || !identifierStart(name[1]) {
			return false
		}
		for row := 2; row < len(name); row++ {
			if !identifierContinue(name[row]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validParameterLiteral(literal public.Literal) bool {
	if !literal.Kind.Valid() {
		return false
	}
	switch literal.Kind {
	case public.ValueKindString:
		return literal.Integer == 0 && !literal.Boolean && utf8.Valid(literal.String)
	case public.ValueKindInteger:
		return len(literal.String) == 0 && !literal.Boolean
	case public.ValueKindBoolean:
		return len(literal.String) == 0 && literal.Integer == 0
	default:
		return false
	}
}

func identifierStart(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value == '_'
}

func identifierContinue(value byte) bool {
	return identifierStart(value) || value >= '0' && value <= '9'
}

func cloneBindingSet(bindings public.BindingSet) public.BindingSet {
	bindings.Fields = append([]public.Binding(nil), bindings.Fields...)
	return bindings
}

func cloneParameters(parameters []Parameter) []Parameter {
	if parameters == nil {
		return nil
	}
	cloned := make([]Parameter, len(parameters))
	for row := range parameters {
		cloned[row] = parameters[row]
		cloned[row].Value.String = append([]byte(nil), parameters[row].Value.String...)
	}
	return cloned
}
