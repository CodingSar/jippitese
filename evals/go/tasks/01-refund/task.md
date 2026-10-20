project: orders
package: billing
---
Add `func Refund(acc Account, amt Money) Account` to package `billing`. It
adds `amt` back to the account's balance. Like `Withdraw`, it panics when
`amt` is not positive.
