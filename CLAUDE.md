# jippitese

A programming language for AI agents. The repo holds no code yet: the language's shape is being designed through `/grill-with-docs`, and nothing is built until `docs/adr/` records the decisions that shape it.

## Commands

None yet. The implementation stack is an open design decision. When it lands, record install, test, lint, and typecheck here as copy-paste commands and delete this paragraph.

## Workflow

1. **Design** with `/grill-with-docs` before any non-trivial build. It runs `grilling` and `domain-modeling`: resolved terms land in `GLOSSARY.md`, hard-to-reverse choices in `docs/adr/`.
2. **Plan** with `/to-spec`, then `/to-tickets` into Backlog.md: one Backlog task per ticket (`backlog task create`, files in `backlog/tasks/`).
3. **Build** one ticket at a time with `/implement`, using `tdd` at seams agreed with the human first. Run `/code-review` before committing.
4. **Ship** by committing to the current branch with the Backlog task id in the message. Open a PR only when asked (`/pr`).

## Domain docs

- Read `GLOSSARY.md` and `docs/adr/` before exploring or naming anything. If they don't exist yet, proceed silently.
- Use the glossary's term for every concept you name; a word listed under _Avoid_ stays out of code, tests, and tickets. A concept missing from the glossary is a gap to raise with `domain-modeling`, not a licence to coin a word.
- When a change contradicts an ADR, say so in the output ("Contradicts ADR-0003 because…") and let the human reopen it.
- Layout is single-context: one root `GLOSSARY.md`; ADRs are `docs/adr/NNNN-slug.md`, numbered from the highest existing one.

## Decisions

Ask the human through the `request_decision` MCP tool (the Decisions app), not AskUserQuestion. If the tool is missing or errors, ask in the terminal. The app owns `.decisions/`; read it to see what is pending, edit it only through the tool.

## Tools

- Library docs: Context7 MCP, not memory.
- Tasks: Backlog.md (`backlog` CLI, tasks in `backlog/`, config in `backlog/config.yml`).
- Architecture: Groma (`groma agent-instructions`); never edit its files directly (`groma/`).
- Public plugins and docs: Tessl MCP; `.tessl/` and `AGENTS.md` are Tessl-managed.
- Engineering skills: the mattpocock-skills plugin, enabled in `.claude/settings.json`. Writing or editing this file or any skill: `writing-for-agents`.

## Gotchas

- The Decisions server (`.mcp.json`) listens on localhost only, so it is unreachable from cloud sessions; fall back to the terminal as the rule above says.
- `groma`, `backlog`, and `tessl` are host-installed CLIs, not project dependencies. A fresh container lacks them; say so instead of improvising their output.

<!-- groma:start -->
## Groma

This project uses Groma. Before you scan, inspect, or curate architecture, or change files for a Backlog task, run `groma agent-instructions` and read the guide it names for that job. When it reports a first scan, ask the user whether they want you to curate the architecture. Do not edit Groma-owned architecture files directly.
<!-- groma:end -->

@AGENTS.md
