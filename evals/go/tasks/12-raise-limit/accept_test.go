package flow

import (
	"errors"
	"testing"
)

func TestAccept12RefundsUpTo20000AreAllowed(t *testing.T) {
	w, _, _ := newFake(Approve)
	if p, err := run(w, RefundReq{Order: 21, Amount: 15_000}); err != nil || p.Amount != 15_000 {
		t.Errorf("Run = %+v, %v; want 15000", p, err)
	}
	_, err := run(w, RefundReq{Order: 22, Amount: 20_001})
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Amount != 20_001 {
		t.Errorf("err = %v, want TooLargeError{20001}", err)
	}
}
