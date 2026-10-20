package orders

import "testing"

func TestAccept06OrdersAbove100000AreNotCounted(t *testing.T) {
	ix := NewIndexer()
	ix.Deliver(Placed{ID: 20, Customer: 20, Total: 200_000})
	ix.Deliver(Placed{ID: 21, Customer: 20, Total: 100_000})
	if got, ok := ix.Totals.Get(20); !ok || got != 100_000 {
		t.Errorf("total = %d, %v; want 100000", got, ok)
	}
}
