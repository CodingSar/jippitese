package billing

import "testing"

func TestAccept04TotalSubtractsLineDiscounts(t *testing.T) {
	lines := []Line{{Price: 100, Qty: 2, Discount: 30}, {Price: 5, Qty: 4, Discount: 0}}
	if got := Total(lines); got != 190 {
		t.Errorf("Total = %d, want 190", got)
	}
}
