/**
 * Contract schema, JSONL append-only event store, and fold logic.
 *
 * v0.7 — stripped to the canonical spec at references/contracts.md (ADR-011).
 *
 * The store is a directory of one JSONL per contract under CONTRACTS_DIR.
 * Every line is a single event with the same shape (see ContractEvent).
 * The first event for a contract_id sets `summary`; subsequent events
 * inherit it. Current state is the fold over events grouped by contract_id.
 *
 * Three statuses: `started | blocked | delivered`. No others.
 *
 * No judge, no parent validation, no cancellation flow, no Drew-approval
 * step. Abandonment is logged as a `delivered` event with a `reasoning`
 * prefixed `abandoned:`.
 */

import {
  appendFileSync,
  closeSync,
  existsSync,
  mkdirSync,
  openSync,
  readFileSync,
  readdirSync,
  unlinkSync,
} from "fs";
import { basename, join } from "path";
import { randomBytes } from "crypto";

// ---- Types -----------------------------------------------------------------

/**
 * Canonical 3-value status (ADR-011 §5). The only legal values.
 */
export type ContractStatus = "started" | "blocked" | "delivered";

/**
 * One event in a contract's append-only log. Matches the wire format in
 * references/contracts.md.
 *
 * Field rules:
 *   - timestamp / contract_id / status / created_by / reasoning are
 *     required on every event.
 *   - summary is set on the FIRST event for a contract_id; null on
 *     subsequent events (the fold inherits).
 *   - owner is set on the first event (claims ownership); subsequent
 *     events MAY include it to record a handoff.
 *   - source is optional; freeform pointer back to the commitment origin.
 */
export interface ContractEvent {
  timestamp: string;
  contract_id: string;
  status: ContractStatus;
  summary: string | null;
  reasoning: string;
  owner: string | null;
  created_by: string;
  source: string | null;
}

/**
 * Folded current state of a contract. Computed on read.
 */
export interface FoldedContract {
  contract_id: string;
  summary: string;
  status: ContractStatus;
  owner: string | null;
  reasoning: string;
  created_by: string;
  source: string | null;
  created_at: string;
  updated_at: string;
  events: ContractEvent[];
}

// ---- Filesystem layout -----------------------------------------------------

/**
 * Resolve the contracts directory from env at every call. Reading at
 * module-load time would cap the dir to whatever was set on first import,
 * which breaks tests that swap CONTRACTS_DIR between describe blocks.
 */
export function contractsDir(): string {
  return (
    process.env.CONTRACTS_DIR || join(process.env.HOME!, ".claude/contracts")
  );
}

/** Backwards-compatible export for any consumer reading the static value. */
export const CONTRACTS_DIR = contractsDir();

function ensureDir() {
  const dir = contractsDir();
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
}

/**
 * Resolve the on-disk JSONL path for a contract id. Filenames are
 * `<id>--<slug>.jsonl`; legacy `<id>.jsonl` is still recognised on read.
 * Resolution scans the contracts dir (id is unique; slug is presentation).
 */
export function contractPath(id: string): string {
  ensureDir();
  const dir = contractsDir();
  const legacy = join(dir, `${id}.jsonl`);
  if (existsSync(legacy)) return legacy;
  for (const fname of readdirSync(dir)) {
    if (fname.startsWith(`${id}--`) && fname.endsWith(".jsonl")) {
      return join(dir, fname);
    }
  }
  return legacy;
}

export function newContractFilename(id: string, summary: string): string {
  const slug = slugify(summary);
  return slug ? `${id}--${slug}.jsonl` : `${id}.jsonl`;
}

export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 50)
    .replace(/-+$/, "");
}

export function idFromFilename(name: string): string {
  const trimmed = basename(name).replace(/\.jsonl$/, "");
  const dash = trimmed.indexOf("--");
  return dash > 0 ? trimmed.slice(0, dash) : trimmed;
}

// ---- ID + timestamp helpers ------------------------------------------------

export function newContractId(): string {
  const now = new Date();
  const yyyy = now.getUTCFullYear().toString();
  const mm = String(now.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(now.getUTCDate()).padStart(2, "0");
  const short = randomBytes(4).toString("hex");
  return `C-${yyyy}-${mm}-${dd}-${short}`;
}

let lastTimestampMs = 0;

export function nowIso(): string {
  let ms = Date.now();
  if (ms <= lastTimestampMs) ms = lastTimestampMs + 1;
  lastTimestampMs = ms;
  return new Date(ms).toISOString();
}

// ---- Append with file lock -------------------------------------------------

function appendLineLocked(filePath: string, line: string) {
  ensureDir();
  const lockPath = `${filePath}.lock`;
  const deadline = Date.now() + 5_000;
  let lockFd: number | null = null;
  while (Date.now() < deadline) {
    try {
      lockFd = openSync(lockPath, "wx");
      break;
    } catch (e: any) {
      if (e.code === "EEXIST") {
        try {
          const st = require("fs").statSync(lockPath);
          if (Date.now() - st.mtimeMs > 30_000) {
            unlinkSync(lockPath);
            continue;
          }
        } catch {}
        Bun.sleepSync ? Bun.sleepSync(20) : null;
      } else {
        throw e;
      }
    }
  }
  if (lockFd === null) {
    throw new Error(`Could not acquire lock for ${filePath} within 5s`);
  }
  try {
    appendFileSync(filePath, line.endsWith("\n") ? line : line + "\n");
  } finally {
    closeSync(lockFd);
    try {
      unlinkSync(lockPath);
    } catch {}
  }
}

// ---- Create / append / read ------------------------------------------------

export function appendEvent(event: ContractEvent): void {
  const path = contractPath(event.contract_id);
  // First event creates the file with a slugged name when summary is set.
  if (!existsSync(path)) {
    if (!event.summary) {
      throw new Error(
        `First event for ${event.contract_id} must include summary`
      );
    }
    const target = join(
      contractsDir(),
      newContractFilename(event.contract_id, event.summary)
    );
    appendLineLocked(target, JSON.stringify(event));
    renderMirror(event.contract_id);
    return;
  }
  appendLineLocked(path, JSON.stringify(event));
  renderMirror(event.contract_id);
}

function renderMirror(contractId: string): void {
  try {
    const folded = readContract(contractId);
    const { renderContractMarkdown } = require("./render");
    renderContractMarkdown(folded);
  } catch (e: any) {
    process.stderr.write(
      `[contracts] render failed for ${contractId}: ${e?.message ?? e}\n`
    );
  }
}

export function readContract(contractId: string): FoldedContract {
  const path = contractPath(contractId);
  if (!existsSync(path)) {
    throw new Error(`Contract ${contractId} not found`);
  }
  const lines = readFileSync(path, "utf8")
    .split("\n")
    .filter((l) => l.length > 0);
  if (lines.length === 0) {
    throw new Error(`Contract file ${path} is empty`);
  }
  const events: ContractEvent[] = [];
  for (let i = 0; i < lines.length; i++) {
    try {
      events.push(JSON.parse(lines[i]) as ContractEvent);
    } catch (e) {
      process.stderr.write(
        `[contracts] WARN: skipping torn line ${i + 1} of ${path}: ${
          (e as any)?.message ?? e
        }\n`
      );
    }
  }
  return foldEvents(contractId, events);
}

function foldEvents(
  contractId: string,
  events: ContractEvent[]
): FoldedContract {
  if (events.length === 0) {
    throw new Error(`No events for ${contractId}`);
  }
  const first = events[0];
  let summary = first.summary ?? "";
  let status = first.status;
  let owner = first.owner ?? null;
  let reasoning = first.reasoning;
  let created_by = first.created_by;
  let source = first.source ?? null;
  const created_at = first.timestamp;
  let updated_at = first.timestamp;

  for (let i = 1; i < events.length; i++) {
    const e = events[i];
    updated_at = e.timestamp;
    if (e.summary !== null && e.summary !== undefined && e.summary !== "") {
      summary = e.summary;
    }
    if (e.status) status = e.status;
    if (e.owner !== null && e.owner !== undefined) owner = e.owner;
    if (e.reasoning) reasoning = e.reasoning;
    if (e.created_by) created_by = e.created_by;
    if (e.source !== null && e.source !== undefined) source = e.source;
  }

  return {
    contract_id: contractId,
    summary,
    status,
    owner,
    reasoning,
    created_by,
    source,
    created_at,
    updated_at,
    events,
  };
}

// ---- Listing ---------------------------------------------------------------

export interface ListFilter {
  status?: ContractStatus;
  owner?: string;
}

export function listContracts(filter: ListFilter = {}): FoldedContract[] {
  ensureDir();
  const out: FoldedContract[] = [];
  for (const fname of readdirSync(contractsDir())) {
    if (!fname.endsWith(".jsonl") || fname.startsWith("_") || fname.startsWith(".")) {
      continue;
    }
    const id = idFromFilename(fname);
    let folded: FoldedContract;
    try {
      folded = readContract(id);
    } catch {
      continue;
    }
    if (filter.status !== undefined && folded.status !== filter.status) continue;
    if (filter.owner !== undefined && folded.owner !== filter.owner) continue;
    out.push(folded);
  }
  out.sort((a, b) => (a.updated_at > b.updated_at ? -1 : 1));
  return out;
}
