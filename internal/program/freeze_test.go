package program

import "testing"

func TestFreezeRejectsInvalidResultTables(t *testing.T) {
	if _, err := Freeze(&Program{}); err == nil {
		t.Fatal("Freeze accepted empty result tables")
	}
}
