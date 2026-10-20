// report.mjs aggregates agent transcripts into the decisions §13 metric.
//
//   node report.mjs out/
//
// expects out/<language>/<model>/<task>.jsonl (language is `jip` or `go`)
// and prints, per language and model, the tokens read and iterations to
// the first green check for each task, with the median over tasks. A run
// that never turned green is reported as such and excluded from medians.
// Runs written by run.mjs also report whether the hidden acceptance tests
// passed and whether a server-side fallback model served any turn.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { measureTranscript, TOKENIZER } from "./count.mjs";

function median(xs) {
  if (xs.length === 0) return null;
  const s = [...xs].sort((a, b) => a - b);
  const m = Math.floor(s.length / 2);
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

const dir = process.argv[2];
if (!dir) {
  console.error("usage: node report.mjs <transcripts dir>");
  process.exit(2);
}
const rows = [];
for (const lang of readdirSync(dir)) {
  const ld = join(dir, lang);
  if (!statSync(ld).isDirectory()) continue;
  for (const model of readdirSync(ld)) {
    const md = join(ld, model);
    if (!statSync(md).isDirectory()) continue;
    const runs = [];
    for (const f of readdirSync(md).filter((f) => f.endsWith(".jsonl")).sort()) {
      const lines = readFileSync(join(md, f), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
      const result = lines.find((l) => l.kind === "result");
      runs.push({
        task: f.replace(/\.jsonl$/, ""), ...measureTranscript(lines),
        ...(result ? { accept: result.accept, fallback: result.fallback } : {}),
      });
    }
    const green = runs.filter((r) => r.green);
    rows.push({
      language: lang, model, tasks: runs.length, green: green.length,
      accepted: runs.filter((r) => r.accept).length,
      median_tokens: median(green.map((r) => r.tokens)),
      median_iterations: median(green.map((r) => r.iterations)),
      runs,
    });
  }
}
console.log(JSON.stringify({ tokenizer: TOKENIZER, results: rows }, null, 2));
