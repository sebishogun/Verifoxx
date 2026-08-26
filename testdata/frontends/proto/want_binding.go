package policy

import "github.com/sebishogun/nornrune/frontend"

const WantPolicyRequestCEL = `teamName == "blue" && count >= 2 && enabled`

var WantPolicyRequestBindingSet = frontend.BindingSet{
	Name:    "access-policy",
	Version: "v1",
	Fields: []frontend.Binding{
		{Source: "teamName", Target: "subject.team", Kind: frontend.ValueKindString, Group: frontend.FieldGroupSubject},
		{Source: "count", Target: "context.count", Kind: frontend.ValueKindInteger, Group: frontend.FieldGroupContext},
		{Source: "enabled", Target: "context.enabled", Kind: frontend.ValueKindBoolean, Group: frontend.FieldGroupContext},
	},
}
