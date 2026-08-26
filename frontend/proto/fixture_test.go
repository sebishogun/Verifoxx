package frontproto_test

import (
	"reflect"
	"testing"

	fixture "github.com/sebishogun/verifoxx/testdata/frontends/proto"
)

func TestCheckedInFixtureMatchesGoldenBinding(t *testing.T) {
	if fixture.PolicyRequestCEL != fixture.WantPolicyRequestCEL {
		t.Fatalf("PolicyRequestCEL = %q, want %q", fixture.PolicyRequestCEL, fixture.WantPolicyRequestCEL)
	}
	if !reflect.DeepEqual(fixture.PolicyRequestBindingSet, fixture.WantPolicyRequestBindingSet) {
		t.Fatalf("PolicyRequestBindingSet = %+v, want %+v", fixture.PolicyRequestBindingSet, fixture.WantPolicyRequestBindingSet)
	}
}
