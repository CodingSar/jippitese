project: sessions
module: sessions.store
breaking:
---
Add `pub fn active_count() -> Result<Int, RepoError> with {mut}` to
`sessions.store`: the number of sessions currently stored.
