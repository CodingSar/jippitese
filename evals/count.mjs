// count.mjs counts tokens for the decisions §13 metric under one named
// tokenizer: o200k_base from js-tiktoken 1.0.21 (pinned in package.json).
//
//   node count.mjs file…              tokens per file and the total
//   node count.mjs --transcript t.jsonl
//
// A transcript is one JSON object per line, in the order the agent saw
// them: {"kind": "read", "path": "…", "text": "…"} for a file it read,
// {"kind": "tool", "cmd": "jip check", "text": "…", "exit": 0} for a tool
// output. Tokens are summed up to and including the first `jip check` (or
// `go vet` for the Go baseline) that exits 0; iterations are the number
// of check runs up to that point.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { getEncoding } from "js-tiktoken";

export const TOKENIZER = "o200k_base (js-tiktoken 1.0.21)";
const enc = getEncoding("o200k_base");

export function tokens(text) {
  return enc.encode(text).length;
}

export function measureTranscript(lines) {
  let total = 0, iterations = 0, green = false;
  for (const ev of lines) {
    total += tokens(ev.text ?? "");
    const isCheck = ev.kind === "tool" && /^(jip check|go vet)\b/.test(ev.cmd ?? "");
    if (isCheck) {
      iterations++;
      if (ev.exit === 0) { green = true; break; }
    }
  }
  return { tokenizer: TOKENIZER, tokens: total, iterations, green };
}

function main() {
  const args = process.argv.slice(2);
  if (args[0] === "--transcript") {
    const lines = readFileSync(args[1], "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
    console.log(JSON.stringify(measureTranscript(lines)));
  } else if (args.length > 0) {
    let sum = 0;
    for (const p of args) {
      const n = tokens(readFileSync(p, "utf8"));
      sum += n;
      console.log(`${n}\t${p}`);
    }
    console.log(`${sum}\ttotal (${TOKENIZER})`);
  } else {
    console.error("usage: node count.mjs file… | --transcript t.jsonl");
    process.exit(2);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
