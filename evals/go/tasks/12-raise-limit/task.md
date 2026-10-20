project: refunds
package: flow
---
Raise the refund limit in package `flow` from 10_000 to 20_000: refunds
above 20_000 fail with `*TooLargeError`, anything up to it follows the
normal approval rules.
