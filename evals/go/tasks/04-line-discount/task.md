project: orders
package: billing
---
Give every order line a discount: add a field `Discount Money` (JSON
`discount`) to `Line` in package `billing`, and make `Total` subtract each
line's discount from that line's `Price * Qty`.
