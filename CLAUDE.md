# jippitese

## Commands
- Install, test, lint: fill in.

## Decisions
- Ask the human through the `request_decision` MCP tool (the Decisions app), not AskUserQuestion. If the tool is missing or errors, ask in the terminal.

## Tools
- Library docs: Context7 MCP, not memory.
- Tasks: Backlog.md (`backlog` CLI, tasks in `backlog/`).
- Architecture: Groma (`groma agent-instructions`); never edit its files directly.
- Public plugins and docs: Tessl MCP.
- Feature design: the `grilling` and `domain-modeling` skills (mattpocock-skills).

<!-- groma:start -->
## Groma

This project uses Groma. Before you scan, inspect, or curate architecture, or change files for a Backlog task, run `groma agent-instructions` and read the guide it names for that job. When it reports a first scan, ask the user whether they want you to curate the architecture. Do not edit Groma-owned architecture files directly.
<!-- groma:end -->

@AGENTS.md
