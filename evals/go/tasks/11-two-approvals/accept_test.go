package flow

import (
	"errors"
	"testing"
)

func TestAccept11LargeRefundsNeedTwoApprovals(t *testing.T) {
	w, approvals, _ := newFake(Approve, Approve)
	p, err := run(w, RefundReq{Order: 11, Amount: 6_000})
	if err != nil || p.Amount != 6_000 {
		t.Errorf("Run = %+v, %v; want 6000", p, err)
	}
	if len(approvals.asked) != 2 {
		t.Errorf("asked %d approvals, want 2", len(approvals.asked))
	}
}

func TestAccept11TheSecondApproverCanDecline(t *testing.T) {
	w, _, _ := newFake(Approve, Deny)
	if _, err := run(w, RefundReq{Order: 12, Amount: 6_000}); !errors.Is(err, ErrDeclined) {
		t.Errorf("err = %v, want ErrDeclined", err)
	}
}

func TestAccept11MidSizeRefundsKeepOneApproval(t *testing.T) {
	w, approvals, _ := newFake(Approve)
	if _, err := run(w, RefundReq{Order: 13, Amount: 900}); err != nil {
		t.Error(err)
	}
	if len(approvals.asked) != 1 {
		t.Errorf("asked %d approvals, want 1", len(approvals.asked))
	}
}
