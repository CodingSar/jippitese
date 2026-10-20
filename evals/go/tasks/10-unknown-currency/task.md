project: rates
package: cache
---
`Quote` in package `cache` answers 10000 for any unknown currency. Make
unknown currencies an error instead: `Quote` returns `(int, bool)` (false
for anything but EUR and GBP), and `Rate` returns `(int, error)`, failing
with a new `*UnknownCurrencyError` whose `Code` field carries the currency
code.
