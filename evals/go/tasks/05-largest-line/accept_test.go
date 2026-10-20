package billing

import "testing"

func TestAccept05LargestLineByLineTotal(t *testing.T) {
	o := Order{ID: 5, Lines: []Line{{Price: 10, Qty: 3}, {Price: 25, Qty: 1}}, Total: 55}
	if l, ok := LargestLine(o); !ok || l != (Line{Price: 10, Qty: 3}) {
		t.Errorf("LargestLine = %+v, %v; want {10 3}", l, ok)
	}
	if l, ok := LargestLine(Order{ID: 6}); ok {
		t.Errorf("LargestLine of an empty order = %+v, want none", l)
	}
}
