package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPolicyNotificationEncoding(t *testing.T) {
	hash := sha256.Sum256([]byte("policy-notification"))
	payload := hex.EncodeToString(hash[:])
	for _, encoded := range []string{payload, strings.ToUpper(payload)} {
		got, ok := decodePolicyNotificationHash(encoded)
		if !ok || got != hash {
			t.Fatalf("decodePolicyNotificationHash(%q) = (%x, %t), want (%x, true)", encoded, got, ok, hash)
		}
	}
	for _, invalid := range []string{"", payload[:len(payload)-1], payload[:len(payload)-1] + "z"} {
		if got, ok := decodePolicyNotificationHash(invalid); ok || got != [sha256.Size]byte{} {
			t.Fatalf("decodePolicyNotificationHash(%q) = (%x, %t), want zero, false", invalid, got, ok)
		}
	}

	first := policyNotificationChannel("nornrune")
	second := policyNotificationChannel("other-policy")
	if first == second || len(first) > 63 || !strings.HasPrefix(first, policyNotificationChannelPrefix) {
		t.Fatalf("policy notification channels = (%q, %q)", first, second)
	}
	for _, value := range first[len(policyNotificationChannelPrefix):] {
		if !strings.ContainsRune("0123456789abcdef", value) {
			t.Fatalf("policy notification channel %q contains unsafe byte %q", first, value)
		}
	}

	if allocations := testing.AllocsPerRun(1000, func() {
		decoded, ok := decodePolicyNotificationHash(payload)
		if !ok || decoded != hash {
			panic("policy notification hash mismatch")
		}
	}); allocations != 0 {
		t.Fatalf("decodePolicyNotificationHash allocations = %v, want 0", allocations)
	}
}
