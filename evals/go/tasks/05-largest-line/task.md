project: orders
package: billing
---
Add `func LargestLine(o Order) (Line, bool)` to package `billing`: the line
with the greatest `Price * Qty`, or false for an order without lines. Use
the package's `MaxBy`.
