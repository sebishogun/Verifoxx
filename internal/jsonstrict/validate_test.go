package jsonstrict

import "testing"

func TestValidateRejectsMalformedUTF8BeforeNormalization(t *testing.T) {
	for _, source := range [][]byte{
		{'"', 0xff, '"'},
		{'{', '"', 0xc0, 0x80, '"', ':', '1', '}'},
		{'[', '"', 0xe2, 0x82, '"', ']'},
	} {
		if err := Validate(source, 8); err == nil {
			t.Fatalf("Validate(%q) error = nil", source)
		}
	}
	if err := Validate([]byte(`"\ufffd"`), 8); err != nil {
		t.Fatalf("Validate(valid replacement rune) error = %v", err)
	}
}
