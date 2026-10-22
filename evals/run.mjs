// run.mjs runs an agent over the frozen tasks and writes the transcripts
// report.mjs aggregates into the decisions §13 metric.
//
//   node run.mjs --lang jip|go [--model m,…] [--task id …] [--max-turns n]
//                [--effort e] [--no-fallback] [--via api|claude-code] [--out dir]
//
// Each run is one model solving one task in a fresh copy of the project
// with the acceptance tests hidden, through five tools: list_dir,
// read_file, write_file, edit_file and run (an allow-listed command, no
// shell). Every text the agent receives is logged in order to
// out/<lang>/<model>/<task>.jsonl: the prompt, each file read, each tool
// output. When the agent stops, the hidden acceptance tests are added and
// run, and a final {"kind": "result"} line records whether they passed
// together with the API (Application Programming Interface) usage.
//
// Models default to the current Claude models and need ANTHROPIC_API_KEY
// (or another credential the Anthropic SDK resolves). `--via claude-code`
// runs the agent through `claude -p` instead, on whatever account Claude
// Code is signed in with: the same system prompt and the same five tools,
// served by mcp.mjs, so the transcript and the metric are the same; the
// usage it records includes Claude Code's own overhead, and server-side
// fallbacks are not requested. The pseudo-model `reference` needs none:
// it replays the reference solution, which tests the harness end to end
// offline.
import Anthropic from "@anthropic-ai/sdk";
import { spawn, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { measureTranscript } from "./count.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = dirname(here);
const jip = join(here, "out", "jip");

const MODELS = ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"];
const MAX_OUTPUT = 50_000; // characters of one tool output the agent sees

const LANGS = {
  jip: {
    tasks: join(here, "tasks"),
    project: (p) => join(root, "examples", p),
    commands: { jip: ["check", "context", "test", "fmt"] },
    check: "jip check",
    system: () => `You are changing a jippitese project. jippitese is a programming language whose source files end in .jip; its language card follows.

${readFileSync(join(root, "docs", "spec", "00-language-card.md"), "utf8")}
You work through tools: list_dir, read_file, write_file, edit_file, and run, which runs one of \`jip check\`, \`jip context <module>\`, \`jip test\` and \`jip fmt\` in the project root. \`jip context <module>\` prints a module's context pack: its public items and what it depends on. When a change alters a module's public interface, run \`jip check --write-interfaces\`; a deliberately breaking change also needs \`--accept-breaking <module>\`. The change is done when \`jip check\` and \`jip test\` both pass. Hidden acceptance scenarios run after you finish. Finish with a one-line summary.`,
  },
  go: {
    tasks: join(here, "go", "tasks"),
    project: (p) => join(here, "go", p),
    commands: { go: ["vet", "test", "build", "doc"], gofmt: null },
    check: "go vet ./...",
    system: () => `You are changing a Go module. You work through tools: list_dir, read_file, write_file, edit_file, and run, which runs one of \`go vet\`, \`go test\`, \`go build\`, \`go doc\` and \`gofmt\` in the module root. The change is done when \`go vet ./...\` and \`go test ./...\` both pass and gofmt is clean. Hidden acceptance tests run after you finish. Finish with a one-line summary.`,
  },
};

export const TOOLS = [
  { name: "list_dir", description: "List a directory of the project. Directories end in /.",
    input_schema: { type: "object", properties: { path: { type: "string", description: "Relative to the project root; . for the root." } }, required: ["path"] } },
  { name: "read_file", description: "Read a file of the project.",
    input_schema: { type: "object", properties: { path: { type: "string" } }, required: ["path"] } },
  { name: "write_file", description: "Create or overwrite a file of the project.",
    input_schema: { type: "object", properties: { path: { type: "string" }, content: { type: "string" } }, required: ["path", "content"] } },
  { name: "edit_file", description: "Replace the one occurrence of old with new in a file of the project.",
    input_schema: { type: "object", properties: { path: { type: "string" }, old: { type: "string" }, new: { type: "string" } }, required: ["path", "old", "new"] } },
  { name: "run", description: "Run an allowed command in the project root, without a shell. Returns its combined output and exit code.",
    input_schema: { type: "object", properties: { cmd: { type: "string", description: "For example: jip check" } }, required: ["cmd"] } },
];

function field(md, name) {
  return md.match(new RegExp(`^${name}: *(.*)$`, "m"))?.[1].trim() ?? "";
}

export function loadTask(lang, id) {
  const dir = join(LANGS[lang].tasks, id);
  const md = readFileSync(join(dir, "task.md"), "utf8");
  return {
    id, dir, project: field(md, "project"), module: field(md, "module"),
    pkg: field(md, "package"), breaking: field(md, "breaking"),
    prompt: md.slice(md.indexOf("\n---\n") + 5).trim(),
  };
}

function workspace(lang, task) {
  const work = mkdtempSync(join(tmpdir(), `jip-eval-${lang}-`));
  cpSync(LANGS[lang].project(task.project), work, { recursive: true });
  rmSync(join(work, ".jip"), { recursive: true, force: true });
  return work;
}

// inside resolves a path the agent names, refusing any outside the project.
function inside(work, p) {
  const abs = resolve(work, p ?? ".");
  if (abs !== work && !abs.startsWith(work + sep)) throw new Error(`${p}: outside the project`);
  return abs;
}

// argv splits a command line on blanks, honouring quotes; shell syntax is
// refused rather than interpreted.
function argv(cmd) {
  if (/[;|&<>$`\\\n]/.test(cmd)) throw new Error("shell syntax is not supported");
  const out = [];
  for (const m of cmd.matchAll(/"([^"]*)"|'([^']*)'|(\S+)/g)) out.push(m[1] ?? m[2] ?? m[3]);
  return out;
}

function exec(work, args) {
  const bin = args[0] === "jip" ? jip : args[0];
  const r = spawnSync(bin, args.slice(1), {
    cwd: work, encoding: "utf8", timeout: 300_000,
    env: { ...process.env, GOTOOLCHAIN: "local" },
  });
  const text = (r.stdout ?? "") + (r.stderr ?? "") + (r.error ? String(r.error) : "");
  return { text, exit: r.status ?? 1 };
}

function clip(text) {
  return text.length <= MAX_OUTPUT ? text : text.slice(0, MAX_OUTPUT) + `\n… (${text.length - MAX_OUTPUT} more characters cut)`;
}

// tool runs one tool call and returns the event to log; its text is what
// the agent receives.
export function tool(lang, work, name, input) {
  try {
    switch (name) {
      case "list_dir": {
        const abs = inside(work, input.path);
        const names = readdirSync(abs).sort().map((n) => (statSync(join(abs, n)).isDirectory() ? n + "/" : n));
        return { kind: "tool", cmd: `ls ${input.path}`, text: names.join("\n"), exit: 0 };
      }
      case "read_file":
        return { kind: "read", path: input.path, text: clip(readFileSync(inside(work, input.path), "utf8")) };
      case "write_file": {
        const abs = inside(work, input.path);
        mkdirSync(dirname(abs), { recursive: true });
        writeFileSync(abs, input.content);
        return { kind: "write", path: input.path, text: `wrote ${input.path}` };
      }
      case "edit_file": {
        const abs = inside(work, input.path);
        const src = readFileSync(abs, "utf8");
        const n = src.split(input.old).length - 1;
        if (n !== 1) throw new Error(`${input.path}: old text occurs ${n} times, want 1`);
        writeFileSync(abs, src.replace(input.old, () => input.new));
        return { kind: "edit", path: input.path, text: `edited ${input.path}` };
      }
      case "run": {
        const args = argv(input.cmd);
        const allowed = LANGS[lang].commands;
        if (!(args[0] in allowed) || (allowed[args[0]] && !allowed[args[0]].includes(args[1]))) {
          throw new Error(`not an allowed command: ${input.cmd}`);
        }
        const { text, exit } = exec(work, args);
        return { kind: "tool", cmd: args.join(" "), text: clip(text), exit };
      }
      default:
        throw new Error(`unknown tool ${name}`);
    }
  } catch (e) {
    return { kind: "error", tool: name, text: e.message, error: true };
  }
}

// accept adds the hidden acceptance tests to the workspace and runs them
// the way validate.sh and validate_go.sh do.
function accept(lang, task, work) {
  if (lang === "jip") {
    const scen = join(work, ...task.module.split("."), "scenarios.jip");
    writeFileSync(scen, (existsSync(scen) ? readFileSync(scen, "utf8") : "") + "\n" + readFileSync(join(task.dir, "accept.jip"), "utf8"));
    return exec(work, ["jip", "test"]);
  }
  cpSync(join(task.dir, "accept_test.go"), join(work, task.pkg, "accept_test.go"));
  const vet = exec(work, ["go", "vet", "./..."]);
  if (vet.exit !== 0) return vet;
  return exec(work, ["go", "test", "-count=1", "./..."]);
}

// reference is a stand-in client that replays the reference solution: it
// reads each file the patch touches, writes its patched contents, runs
// the check and stops.
function reference(lang, task) {
  const work = workspace(lang, task);
  const patch = readFileSync(join(task.dir, "solution.patch"), "utf8");
  spawnSync("patch", ["-s", "-p1"], { cwd: work, input: patch });
  const files = [...patch.matchAll(/^\+\+\+ b\/(\S+)/gm)].map((m) => m[1]);
  const check = lang === "jip"
    ? `jip check --write-interfaces${task.breaking ? ` --accept-breaking ${task.breaking}` : ""}`
    : "go vet ./...";
  const turns = [
    files.map((path) => ({ name: "read_file", input: { path } })),
    files.map((path) => ({ name: "write_file", input: { path, content: readFileSync(join(work, path), "utf8") } })),
    [{ name: "run", input: { cmd: check } }],
  ];
  rmSync(work, { recursive: true, force: true });
  let n = 0;
  const usage = { input_tokens: 0, output_tokens: 0 };
  return {
    beta: { messages: { create: async () => {
      const calls = turns[n++];
      if (!calls) return { model: "reference", stop_reason: "end_turn", usage, content: [{ type: "text", text: "Applied the reference solution." }] };
      return { model: "reference", stop_reason: "tool_use", usage,
        content: calls.map((c, i) => ({ type: "tool_use", id: `ref_${n}_${i}`, ...c })) };
    } } },
  };
}

class Fatal extends Error {}

export async function runTask({ client, lang, model, task, maxTurns, effort, fallback }) {
  const work = workspace(lang, task);
  const system = LANGS[lang].system();
  const events = [{ kind: "prompt", text: system + "\n\n" + task.prompt }];
  const messages = [{ role: "user", content: task.prompt }];
  const usage = { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 };
  const result = { kind: "result", turns: 0, stop: null, served_by: new Set(), fallback: false };
  const started = Date.now();
  try {
    while (result.turns < maxTurns) {
      result.turns++;
      const params = {
        model, max_tokens: 16000, system, tools: TOOLS, tool_choice: { type: "auto" }, messages,
        thinking: { type: "adaptive" }, output_config: { effort },
        cache_control: { type: "ephemeral" },
      };
      if (fallback) Object.assign(params, { betas: ["server-side-fallback-2026-07-01"], fallbacks: "default" });
      let response;
      try {
        response = await client.beta.messages.create(params);
      } catch (e) {
        if (e instanceof Anthropic.AuthenticationError || e instanceof Anthropic.PermissionDeniedError || /authentication method/.test(e.message)) {
          throw new Fatal(`no usable Anthropic credentials (${e.message}); set ANTHROPIC_API_KEY`);
        }
        throw e;
      }
      for (const k of Object.keys(usage)) usage[k] += response.usage?.[k] ?? 0;
      result.served_by.add(response.model);
      if ((response.usage?.iterations ?? []).some((it) => it.type === "fallback_message")) result.fallback = true;
      // The full content goes back unchanged: thinking and fallback blocks
      // must be preserved.
      messages.push({ role: "assistant", content: response.content });
      result.stop = response.stop_reason;
      if (response.stop_reason === "pause_turn") continue;
      if (response.stop_reason !== "tool_use") break;
      const results = [];
      for (const call of response.content.filter((b) => b.type === "tool_use")) {
        const ev = tool(lang, work, call.name, call.input ?? {});
        events.push(ev);
        results.push({ type: "tool_result", tool_use_id: call.id, content: ev.exit === undefined ? ev.text : `${ev.text}\n(exit ${ev.exit})`, ...(ev.error ? { is_error: true } : {}) });
      }
      messages.push({ role: "user", content: results });
    }
    const a = accept(lang, task, work);
    Object.assign(result, { accept: a.exit === 0, accept_output: a.text.slice(-4000) });
  } catch (e) {
    if (e instanceof Fatal) throw e;
    Object.assign(result, { accept: false, error: e instanceof Anthropic.APIError ? `${e.constructor.name}: ${e.message}` : String(e.stack ?? e) });
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
  Object.assign(result, { served_by: [...result.served_by], usage, seconds: Math.round((Date.now() - started) / 1000) });
  events.push(result);
  return events;
}

// runTaskClaudeCode runs one task through `claude -p`: no built-in tools,
// the harness tools over MCP, and no settings, CLAUDE.md or session file.
export async function runTaskClaudeCode({ lang, model, task, maxTurns, effort }) {
  const work = workspace(lang, task);
  const aside = mkdtempSync(join(tmpdir(), "jip-eval-cc-"));
  const log = join(aside, "events.jsonl");
  writeFileSync(log, "");
  const cfg = join(aside, "mcp.json");
  writeFileSync(cfg, JSON.stringify({ mcpServers: { eval: { command: process.execPath, args: [join(here, "mcp.mjs"), lang, work, log] } } }));
  const system = LANGS[lang].system();
  const result = { kind: "result", via: "claude-code", turns: 0, stop: null, served_by: [], fallback: false };
  const started = Date.now();
  try {
    const args = [
      "-p", task.prompt, "--system-prompt", system, "--model", model, "--effort", effort,
      "--tools", "", "--mcp-config", cfg, "--strict-mcp-config",
      "--allowedTools", TOOLS.map((t) => `mcp__eval__${t.name}`).join(","),
      "--max-turns", String(maxTurns), "--output-format", "json",
      "--no-session-persistence", "--session-id", randomUUID(), "--setting-sources", "",
    ];
    const out = await new Promise((done) => {
      const p = spawn("claude", args, { cwd: aside, stdio: ["ignore", "pipe", "pipe"] });
      let stdout = "", stderr = "";
      p.stdout.on("data", (d) => (stdout += d));
      p.stderr.on("data", (d) => (stderr += d));
      const timer = setTimeout(() => p.kill("SIGTERM"), 3_600_000);
      p.on("close", (code) => { clearTimeout(timer); done({ code, stdout, stderr }); });
    });
    let r;
    try {
      r = JSON.parse(out.stdout);
    } catch {
      throw new Error(`claude exited ${out.code}: ${(out.stderr || out.stdout).slice(-2000)}`);
    }
    if (/authenticat|not logged in|log in/i.test(r.result ?? "") && r.is_error) throw new Fatal(`claude -p is not signed in: ${r.result}`);
    Object.assign(result, {
      turns: r.num_turns ?? 0, stop: r.subtype === "success" ? r.stop_reason ?? "end_turn" : r.subtype,
      served_by: Object.keys(r.modelUsage ?? {}), usage: r.usage, cost_usd: r.total_cost_usd,
      ...(r.is_error ? { error: String(r.result ?? r.subtype).slice(0, 2000) } : {}),
    });
    const a = accept(lang, task, work);
    Object.assign(result, { accept: a.exit === 0, accept_output: a.text.slice(-4000) });
  } catch (e) {
    if (e instanceof Fatal) throw e;
    Object.assign(result, { accept: false, error: String(e.stack ?? e) });
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
  const events = [{ kind: "prompt", text: system + "\n\n" + task.prompt },
    ...readFileSync(log, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))];
  rmSync(aside, { recursive: true, force: true });
  Object.assign(result, { seconds: Math.round((Date.now() - started) / 1000) });
  events.push(result);
  return events;
}

function parseArgs(args) {
  const o = { lang: null, models: MODELS, tasks: [], maxTurns: 40, effort: "high", fallback: true, via: "api", out: join(here, "out") };
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--lang") o.lang = args[++i];
    else if (a === "--model") o.models = args[++i].split(",").filter(Boolean);
    else if (a === "--task") o.tasks.push(args[++i]);
    else if (a === "--max-turns") o.maxTurns = Number(args[++i]);
    else if (a === "--effort") o.effort = args[++i];
    else if (a === "--no-fallback") o.fallback = false;
    else if (a === "--via") o.via = args[++i];
    else if (a === "--out") o.out = resolve(args[++i]);
    else throw new Error(`unknown argument ${a}`);
  }
  if (!(o.lang in LANGS)) throw new Error("--lang must be jip or go");
  if (!(o.maxTurns > 0)) throw new Error("--max-turns must be positive");
  if (!["api", "claude-code"].includes(o.via)) throw new Error("--via must be api or claude-code");
  return o;
}

async function main() {
  let o;
  try {
    o = parseArgs(process.argv.slice(2));
  } catch (e) {
    console.error(`${e.message}\nusage: node run.mjs --lang jip|go [--model m,…] [--task id …] [--max-turns n] [--effort e] [--no-fallback] [--via api|claude-code] [--out dir]`);
    process.exit(2);
  }
  if (o.lang === "jip") {
    const b = spawnSync("go", ["build", "-o", jip, "./cmd/jip"], { cwd: join(root, "compiler"), stdio: "inherit" });
    if (b.status !== 0) process.exit(3);
  }
  const ids = o.tasks.length ? o.tasks : readdirSync(LANGS[o.lang].tasks).filter((d) => statSync(join(LANGS[o.lang].tasks, d)).isDirectory()).sort();
  const sdk = o.via === "api" && o.models.some((m) => m !== "reference") ? new Anthropic({ maxRetries: 5 }) : null;
  for (const model of o.models) {
    const dir = join(o.out, o.lang, model);
    for (const id of ids) {
      const task = loadTask(o.lang, id);
      const client = model === "reference" ? reference(o.lang, task) : sdk;
      // Fallbacks are a server-side feature of the Opus and Sonnet models.
      const fallback = o.fallback && /^claude-(opus|sonnet)-/.test(model);
      let events;
      try {
        events = o.via === "claude-code" && model !== "reference"
          ? await runTaskClaudeCode({ lang: o.lang, model, task, maxTurns: o.maxTurns, effort: o.effort })
          : await runTask({ client, lang: o.lang, model, task, maxTurns: o.maxTurns, effort: o.effort, fallback });
      } catch (e) {
        console.error(e.message);
        process.exit(3);
      }
      mkdirSync(dir, { recursive: true });
      writeFileSync(join(dir, `${id}.jsonl`), events.map((e) => JSON.stringify(e)).join("\n") + "\n");
      const m = measureTranscript(events);
      const r = events.at(-1);
      console.log(`${o.lang} ${model} ${id}: ${m.green ? "green" : "never green"} tokens=${m.tokens} iterations=${m.iterations} accept=${r.accept}${r.fallback ? " fallback" : ""}${r.error ? ` error=${r.error.split("\n")[0]}` : ""} → ${relative(process.cwd(), join(dir, `${id}.jsonl`))}`);
    }
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
