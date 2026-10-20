package billing

import "testing"

func TestAccept02RefundedIsAFinalStatus(t *testing.T) {
	if !IsFinal(Refunded) {
		t.Error("IsFinal(Refunded) = false")
	}
	if IsFinal(Pending) {
		t.Error("IsFinal(Pending) = true")
	}
	if !IsFinal(Paid) || !IsFinal(Failed) {
		t.Error("Paid and Failed must be final")
	}
}
