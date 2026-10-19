project: orders
module: orders.billing
breaking:
---
Add `pub fn largest_line(o: Order) -> Option<Line>` to `orders.billing`:
the line with the greatest `price * qty`, or `None` for an order without
lines. Use the module's `max_by`.
