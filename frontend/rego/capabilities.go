package rego

import public "github.com/sebishogun/nornrune/frontend"

var capabilities = [...]public.Capability{
	{Name: "rego_v1_modules", Support: public.SupportSupported},
	{Name: "complete_boolean_decisions", Support: public.SupportSupported},
	{Name: "boolean_defaults", Support: public.SupportSupported},
	{Name: "multiple_rules", Support: public.SupportSupported},
	{Name: "conjunctive_bodies", Support: public.SupportSupported},
	{Name: "static_input_references", Support: public.SupportRestricted},
	{Name: "scalar_comparisons", Support: public.SupportRestricted},
	{Name: "constant_membership", Support: public.SupportRestricted},
	{Name: "presence_aware_negation", Support: public.SupportRestricted},
	{Name: "imports_and_data", Support: public.SupportRejected},
	{Name: "functions_and_recursion", Support: public.SupportRejected},
	{Name: "variables_and_comprehensions", Support: public.SupportRejected},
	{Name: "with_and_unsupported_builtins", Support: public.SupportRejected},
}

// Capabilities returns the stable Rego compatibility matrix.
func Capabilities() []public.Capability {
	result := make([]public.Capability, len(capabilities))
	copy(result, capabilities[:])
	return result
}
