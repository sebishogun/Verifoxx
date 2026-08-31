package natural

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
)

func TestApprovalTokenBinaryRoundTripOwnsBytes(t *testing.T) {
	token := ApprovalToken{
		Reviewer:       []byte("reviewer-1"),
		Signature:      []byte{1, 2, 3, 4},
		ProposalDigest: sha256.Sum256([]byte("proposal")),
		DraftDigest:    sha256.Sum256([]byte("draft")),
		IssuedUnix:     100,
		ExpiresUnix:    200,
		SchemaVersion:  1,
	}
	encoded, err := AppendApprovalToken(nil, token, DefaultLimits())
	if err != nil {
		t.Fatalf("AppendApprovalToken() error = %v", err)
	}
	decoded, err := ParseApprovalToken(encoded, DefaultLimits())
	if err != nil {
		t.Fatalf("ParseApprovalToken() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, token) {
		t.Fatalf("decoded token = %#v, want %#v", decoded, token)
	}
	encoded[len(encoded)-1] ^= 1
	if decoded.Signature[3] != 4 {
		t.Fatal("decoded token borrows encoded signature bytes")
	}
}

func TestApprovalTokenBinaryRejectsMalformedInput(t *testing.T) {
	token := ApprovalToken{
		Reviewer:       []byte("reviewer-1"),
		Signature:      []byte{1, 2, 3, 4},
		ProposalDigest: sha256.Sum256([]byte("proposal")),
		DraftDigest:    sha256.Sum256([]byte("draft")),
		IssuedUnix:     100,
		ExpiresUnix:    200,
		SchemaVersion:  1,
	}
	encoded, err := AppendApprovalToken(nil, token, DefaultLimits())
	if err != nil {
		t.Fatalf("AppendApprovalToken() error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "empty", mutate: func([]byte) []byte { return nil }},
		{name: "truncated", mutate: func(source []byte) []byte { return source[:len(source)-1] }},
		{name: "trailing", mutate: func(source []byte) []byte { return append(source, 0) }},
		{name: "magic", mutate: func(source []byte) []byte { source[0] ^= 1; return source }},
		{name: "version", mutate: func(source []byte) []byte { source[4] = 2; return source }},
		{name: "reviewer length", mutate: func(source []byte) []byte { source[86] = 0xff; return source }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := append([]byte(nil), encoded...)
			_, err := ParseApprovalToken(test.mutate(source), DefaultLimits())
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("ParseApprovalToken() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestAppendApprovalTokenLimitFailureIsAtomic(t *testing.T) {
	token := ApprovalToken{
		Reviewer:      []byte("reviewer-1"),
		Signature:     []byte{1, 2, 3, 4},
		IssuedUnix:    100,
		ExpiresUnix:   200,
		SchemaVersion: 1,
	}
	limits := DefaultLimits()
	limits.MaxTokenBytes = 8
	dst := []byte("prefix")
	got, err := AppendApprovalToken(dst, token, limits)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("AppendApprovalToken() error = %v, want ErrLimit", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("output changed on error: got %q, want %q", got, dst)
	}
}

func FuzzApprovalToken(f *testing.F) {
	f.Add([]byte("NRAT"))
	f.Add(make([]byte, 96))
	f.Fuzz(func(t *testing.T, source []byte) {
		limits := DefaultLimits()
		limits.MaxTokenBytes = 4096
		token, err := ParseApprovalToken(source, limits)
		if err != nil {
			return
		}
		encoded, err := AppendApprovalToken(nil, token, limits)
		if err != nil {
			t.Fatalf("AppendApprovalToken() after parse error = %v", err)
		}
		if !bytes.Equal(encoded, source) {
			t.Fatalf("round trip differs: got %x, want %x", encoded, source)
		}
	})
}
