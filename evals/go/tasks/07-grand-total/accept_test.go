package orders

import "testing"

func TestAccept07GrandTotalSumsEveryCustomer(t *testing.T) {
	ix := NewIndexer()
	ix.Deliver(Placed{ID: 1, Customer: 1, Total: 10})
	ix.Deliver(Placed{ID: 2, Customer: 2, Total: 32})
	if got := ix.GrandTotal(); got != 42 {
		t.Errorf("GrandTotal = %d, want 42", got)
	}
}
