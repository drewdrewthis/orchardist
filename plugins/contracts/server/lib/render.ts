/**
 * Markdown mirror renderer for contracts (v0.7).
 *
 * Every contract has a JSONL event log (source of truth) and a regenerated
 * Markdown file for human / Obsidian browsing. The mirror lives next to
 * the JSONL with the same id--slug stem.
 */

import { readdirSync, unlinkSync, writeFileSync } from "fs";
import { join } from "path";
import {
  ContractEvent,
  FoldedContract,
  contractsDir,
  idFromFilename,
  slugify,
} from "./contract";

export function markdownPath(folded: FoldedContract): string {
  const id = folded.contract_id;
  const slug = slugify(folded.summary);
  const targetName = slug ? `${id}--${slug}.md` : `${id}.md`;
  const dir = contractsDir();
  const target = join(dir, targetName);
  // Clean up stale .md files for this id (slug changed since last render).
  for (const fname of readdirSync(dir)) {
    if (!fname.endsWith(".md")) continue;
    if (idFromFilename(fname) === id && fname !== targetName) {
      try {
        unlinkSync(join(dir, fname));
      } catch {}
    }
  }
  return target;
}

export function renderContractMarkdown(folded: FoldedContract): void {
  writeFileSync(markdownPath(folded), formatMarkdown(folded));
}

function formatMarkdown(folded: FoldedContract): string {
  const lines: string[] = [];
  lines.push(`# ${folded.contract_id} · ${statusEmoji(folded.status)} ${folded.status}`);
  lines.push("");
  lines.push(`> **${folded.summary}**`);
  lines.push("");
  lines.push("| | |");
  lines.push("|---|---|");
  lines.push(`| **Owner** | ${folded.owner ?? "—"} |`);
  lines.push(`| **Created by** | ${folded.created_by} |`);
  if (folded.source) lines.push(`| **Source** | ${folded.source} |`);
  lines.push(`| **Created** | ${formatTime(folded.created_at)} |`);
  lines.push(`| **Updated** | ${formatTime(folded.updated_at)} |`);
  lines.push(`| **Last reasoning** | ${folded.reasoning} |`);
  lines.push("");
  lines.push("## Timeline");
  lines.push("");
  for (const evt of folded.events) {
    lines.push(`- ${formatTime(evt.timestamp)} ${eventLine(evt)}`);
  }
  lines.push("");
  lines.push("---");
  lines.push(
    "_Source of truth: the `.jsonl` file with the same name. This `.md` is regenerated on every event — do not edit directly._"
  );
  return lines.join("\n") + "\n";
}

function statusEmoji(s: string): string {
  switch (s) {
    case "started":
      return "🟡";
    case "blocked":
      return "🟣";
    case "delivered":
      return "✅";
    default:
      return "❓";
  }
}

function eventLine(evt: ContractEvent): string {
  const owner = evt.owner ? ` · owner=${evt.owner}` : "";
  const src = evt.source ? ` · source=${evt.source}` : "";
  return `${statusEmoji(evt.status)} \`${evt.status}\` by ${evt.created_by}${owner}${src} — ${evt.reasoning}`;
}

function formatTime(iso: string): string {
  return iso.replace("T", " ").replace(/\.\d+Z$/, "Z").slice(0, 20);
}
