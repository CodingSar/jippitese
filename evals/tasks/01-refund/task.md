project: orders
module: orders.billing
breaking:
---
Add `pub fn refund(acc: Account, amt: Money) -> Account` to
`orders.billing`. It adds `amt` back to the account's balance. Give it the
precondition `amt.0 > 0` and a postcondition stating that the returned
balance is the old balance plus `amt`.
