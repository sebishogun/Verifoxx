package cedar

import public "github.com/sebishogun/nornrune/frontend"

var capabilities = [...]public.Capability{
	{Name: "static_permit_forbid", Support: public.SupportSupported},
	{Name: "equality_scopes", Support: public.SupportRestricted},
	{Name: "context_scalar_conditions", Support: public.SupportRestricted},
	{Name: "boolean_composition", Support: public.SupportSupported},
	{Name: "forbid_precedence", Support: public.SupportSupported},
	{Name: "entity_hierarchy_and_attributes", Support: public.SupportRejected},
	{Name: "sets_records_and_extensions", Support: public.SupportRejected},
	{Name: "templates_and_annotations", Support: public.SupportRejected},
}

// Capabilities returns the stable Cedar compatibility matrix.
func Capabilities() []public.Capability {
	result := make([]public.Capability, len(capabilities))
	copy(result, capabilities[:])
	return result
}
