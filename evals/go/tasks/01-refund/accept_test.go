package billing

import "testing"

func TestAccept01RefundUndoesAWithdrawal(t *testing.T) {
	acc := Account{ID: 1, Balance: 500}
	a, err := Withdraw(acc, 200)
	if err != nil {
		t.Fatal(err)
	}
	if r := Refund(a, 200); r != acc {
		t.Errorf("Refund = %+v, want %+v", r, acc)
	}
}

func TestAccept01RefundAddsToTheBalance(t *testing.T) {
	if a := Refund(Account{ID: 2, Balance: 0}, 70); a.Balance != 70 {
		t.Errorf("balance = %d, want 70", a.Balance)
	}
}
