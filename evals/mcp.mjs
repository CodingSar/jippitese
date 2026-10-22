// mcp.mjs serves the harness's five tools over MCP (Model Context
// Protocol) on stdio, for runs through Claude Code (`run.mjs --via
// claude-code`). Each tool call runs exactly as in an API run and its
// event is appended to the log, so the transcript is the same text the
// agent receives either way.
//
//   node mcp.mjs <lang> <workspace> <log.jsonl>
import { appendFileSync } from "node:fs";
import { createInterface } from "node:readline";
import { TOOLS, tool } from "./run.mjs";

const [lang, work, log] = process.argv.slice(2);

function send(msg) {
  process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...msg }) + "\n");
}

function handle(method, params) {
  switch (method) {
    case "initialize":
      return { protocolVersion: params?.protocolVersion ?? "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "eval", version: "1" } };
    case "ping":
      return {};
    case "tools/list":
      return { tools: TOOLS.map((t) => ({ name: t.name, description: t.description, inputSchema: t.input_schema })) };
    case "tools/call": {
      const ev = tool(lang, work, params.name, params.arguments ?? {});
      appendFileSync(log, JSON.stringify(ev) + "\n");
      const text = ev.exit === undefined ? ev.text : `${ev.text}\n(exit ${ev.exit})`;
      return { content: [{ type: "text", text }], ...(ev.error ? { isError: true } : {}) };
    }
    default:
      throw Object.assign(new Error(`unknown method ${method}`), { code: -32601 });
  }
}

createInterface({ input: process.stdin }).on("line", (line) => {
  if (!line.trim()) return;
  const msg = JSON.parse(line);
  if (msg.id === undefined) return; // notifications need no reply
  try {
    send({ id: msg.id, result: handle(msg.method, msg.params) });
  } catch (e) {
    send({ id: msg.id, error: { code: e.code ?? -32603, message: e.message } });
  }
});
