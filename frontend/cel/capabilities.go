package cel

import public "github.com/sebishogun/verifoxx/frontend"

var capabilities = [...]public.Capability{
	{Name: "boolean_literals", Support: public.SupportSupported},
	{Name: "scalar_variables", Support: public.SupportSupported},
	{Name: "object_selection", Support: public.SupportRestricted},
	{Name: "scalar_comparisons", Support: public.SupportRestricted},
	{Name: "logical_operators", Support: public.SupportSupported},
	{Name: "constant_list_membership", Support: public.SupportRestricted},
	{Name: "function_calls", Support: public.SupportRejected},
	{Name: "macros_and_comprehensions", Support: public.SupportRejected},
	{Name: "maps_messages_and_optionals", Support: public.SupportRejected},
}

// Capabilities returns the stable CEL compatibility matrix.
func Capabilities() []public.Capability {
	result := make([]public.Capability, len(capabilities))
	copy(result, capabilities[:])
	return result
}
