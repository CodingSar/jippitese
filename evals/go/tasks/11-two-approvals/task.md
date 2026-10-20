project: refunds
package: flow
---
Refunds of 5_000 or more need a second approval in package `flow`: after
the first `Approve`, ask again with the title prefixed "Second approval: ";
a `Deny` at either step declines the refund. Refunds from 500 up to 4_999
keep a single approval. The step sequence changes, so bump the workflow
`Version`.
