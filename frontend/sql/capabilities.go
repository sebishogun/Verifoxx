package sql

import public "github.com/sebishogun/nornrune/frontend"

var postgreSQLCapabilities = [...]public.Capability{
	{Name: "scalar_expressions", Support: public.SupportRestricted},
	{Name: "three_valued_logic", Support: public.SupportRestricted},
	{Name: "compile_time_parameters", Support: public.SupportRestricted},
	{Name: "row_level_security", Support: public.SupportSupported},
	{Name: "permissive_and_restrictive_policies", Support: public.SupportSupported},
	{Name: "casts_and_functions", Support: public.SupportRejected},
	{Name: "queries_and_catalog_access", Support: public.SupportRejected},
}

var snowflakeCapabilities = [...]public.Capability{
	{Name: "scalar_expressions", Support: public.SupportRestricted},
	{Name: "three_valued_logic", Support: public.SupportRestricted},
	{Name: "compile_time_parameters", Support: public.SupportRestricted},
	{Name: "row_level_security", Support: public.SupportRejected},
	{Name: "casts_and_functions", Support: public.SupportRejected},
	{Name: "queries_and_catalog_access", Support: public.SupportRejected},
}

var databricksCapabilities = [...]public.Capability{
	{Name: "scalar_expressions", Support: public.SupportRestricted},
	{Name: "three_valued_logic", Support: public.SupportRestricted},
	{Name: "compile_time_parameters", Support: public.SupportRestricted},
	{Name: "row_level_security", Support: public.SupportRejected},
	{Name: "casts_and_functions", Support: public.SupportRejected},
	{Name: "queries_and_catalog_access", Support: public.SupportRejected},
}

// Capabilities returns an owned copy of one SQL profile's capability matrix.
func Capabilities(dialect Dialect) []public.Capability {
	var source []public.Capability
	switch dialect {
	case DialectPostgreSQL:
		source = postgreSQLCapabilities[:]
	case DialectSnowflake:
		source = snowflakeCapabilities[:]
	case DialectDatabricks:
		source = databricksCapabilities[:]
	default:
		return nil
	}
	result := make([]public.Capability, len(source))
	copy(result, source)
	return result
}
