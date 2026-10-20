project: orders
package: billing
---
Add a `Refunded` value to `Status` in package `billing`, and a
`func IsFinal(s Status) bool` that is true for `Paid`, `Failed` and
`Refunded` and false for `Pending`.
