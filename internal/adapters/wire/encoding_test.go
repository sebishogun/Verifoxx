package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestAppendJSONStringPreservesAdapterEncoding(t *testing.T) {
	tests := []struct {
		value []byte
		want  string
	}{
		{nil, `""`},
		{[]byte("plain ASCII"), `"plain ASCII"`},
		{[]byte("\\\"\b\f\n\r\t<>&\x00\x1f"), `"\\\"\b\f\n\r\t\u003c\u003e\u0026\u0000\u001f"`},
		{[]byte("snowman: \u2603"), "\"snowman: \u2603\""},
		{[]byte("line\u2028paragraph\u2029"), `"line\u2028paragraph\u2029"`},
		{[]byte{0xff, 'x'}, `"\ufffdx"`},
	}
	for _, test := range tests {
		got := AppendJSONString([]byte("prefix:"), test.value)
		if string(got) != "prefix:"+test.want {
			t.Errorf("AppendJSONString(%q) = %q, want %q", test.value, got, "prefix:"+test.want)
		}
	}
}

func TestSHA256HexRoundTrip(t *testing.T) {
	hash := sha256.Sum256([]byte("verifoxx"))
	want := hex.EncodeToString(hash[:])
	if got := string(AppendSHA256(nil, hash)); got != want {
		t.Fatalf("AppendSHA256() = %q, want %q", got, want)
	}
	for _, encoded := range []string{want, strings.ToUpper(want)} {
		got, ok := DecodeSHA256(encoded)
		if !ok || got != hash {
			t.Errorf("DecodeSHA256(%q) = %x, %t, want %x, true", encoded, got, ok, hash)
		}
	}
	for _, encoded := range []string{"", want[:63], want[:63] + "g"} {
		got, ok := DecodeSHA256(encoded)
		if ok || got != [sha256.Size]byte{} {
			t.Errorf("DecodeSHA256(%q) = %x, %t, want zero, false", encoded, got, ok)
		}
	}
}

func TestWireEncodingWarmPathDoesNotAllocate(t *testing.T) {
	hash := sha256.Sum256([]byte("verifoxx"))
	encoded := hex.EncodeToString(hash[:])
	stringDst := make([]byte, 0, 128)
	hashDst := make([]byte, 0, sha256.Size*2)
	var decoded [sha256.Size]byte
	if allocations := testing.AllocsPerRun(100, func() {
		stringDst = AppendJSONString(stringDst[:0], []byte("<verifoxx>"))
		hashDst = AppendSHA256(hashDst[:0], hash)
		decoded, _ = DecodeSHA256(encoded)
	}); allocations != 0 {
		t.Fatalf("wire encoding warm allocations = %f, want 0", allocations)
	}
	if decoded != hash || len(stringDst) == 0 || len(hashDst) != sha256.Size*2 {
		t.Fatal("wire encoding allocation check did not retain results")
	}
}
