#!/usr/bin/env bun
/**
 * Migrate contract JSONL files from the v0.6 shape (record + events) to the
 * v0.7 shape (append-only events with the spec's flat schema).
 *
 * Idempotent: a contract whose line 1 is already a v0.7 event (has
 * `created_by`) is skipped. Otherwise the script:
 *
 *   1. Reads the old line-1 ContractRecord and any subsequent events.
 *   2. Computes the v0.6 final state (rich status + closed_on_reason +
 *      cancellation reason if any).
 *   3. Maps it to v0.7:
 *        open                                  → started
 *        delivered_pending_validation
 *        delivered_pending_parent_validation
 *        pending_drew_approval
 *        satisfied                             → delivered (judges had passed)
 *        awaiting_cancel_ack                   → delivered (abandoned: cancel ack never came)
 *        cancelled                             → delivered (abandoned: <prior cancel reason>)
 *        judge_rejected_terminal               → delivered (abandoned: judge retired)
 *   4. Builds a v0.7 starter event (`started`) carrying the contract's
 *      summary + owner + source from line 1, plus a single follow-up event
 *      reflecting the migrated final state.
 *   5. Atomically rewrites the file.
 *
 * Owner is reconstructed as `<vm_address>:claude:<session_id>` from the v0.6
 * Owner triple. Source becomes `migration:v06-to-v07` so consumers can
 * audit. The `created_by` field is `migrate-v06-to-v07` for both events;
 * idempotency check looks for that exact value.
 *
 * Usage:
 *   bun run scripts/migrate-v06-to-v07.ts            # default: ~/.claude/contracts
 *   CONTRACTS_DIR=/abs/path bun run scripts/migrate-v06-to-v07.ts
 *
 * Exit codes:
 *   0 — completed (some files may have been skipped as already migrated)
 *   2 — internal error
 */

import {
  existsSync,
  readFileSync,
  readdirSync,
  renameSync,
  writeFileSync,
} from "fs";
import { join } from "path";

function defaultContractsDir(): string {
  return (
    process.env.CONTRACTS_DIR || join(process.env.HOME!, ".claude/contracts")
  );
}

type V07Event = {
  timestamp: string;
  contract_id: string;
  status: "started" | "blocked" | "delivered";
  summary: string | null;
  reasoning: string;
  owner: string | null;
  created_by: string;
  source: string | null;
};

const MIGRATION_CREATED_BY = "migrate-v06-to-v07";

interface V06Owner {
  session_id?: string;
  agent_name?: string;
  vm_address?: string;
}

function ownerString(o?: V06Owner): string | null {
  if (!o) return null;
  const machine = o.vm_address ?? "unknown";
  const project = "claude";
  const sid = o.session_id ?? "unknown";
  return `${machine}:${project}:${sid}`;
}

interface MigratedFinalState {
  status: "started" | "delivered";
  reasoning: string;
}

function mapFinalState(
  v06Status: string,
  closed_on_reason: string | null | undefined,
  abandonReason: string | null
): MigratedFinalState {
  switch (v06Status) {
    case "open":
      return {
        status: "started",
        reasoning: "migrated open → started (v0.6 → v0.7)",
      };
    case "delivered_pending_validation":
    case "delivered_pending_parent_validation":
      return {
        status: "delivered",
        reasoning:
          "migrated from delivered_pending_parent_validation (v0.6 → v0.7); validation step retired",
      };
    case "pending_drew_approval":
      return {
        status: "delivered",
        reasoning:
          "migrated from pending_drew_approval (v0.6 → v0.7); judges had passed",
      };
    case "satisfied":
      return {
        status: "delivered",
        reasoning:
          "migrated from satisfied (v0.6 → v0.7); judges had passed",
      };
    case "awaiting_cancel_ack":
      return {
        status: "delivered",
        reasoning:
          "abandoned: migrated from awaiting_cancel_ack (v0.6 → v0.7); cancel ack never came",
      };
    case "cancelled": {
      const tail = abandonReason ?? closed_on_reason ?? "cancelled in v0.6";
      return {
        status: "delivered",
        reasoning: `abandoned: migrated from cancelled (v0.6 → v0.7); ${tail}`,
      };
    }
    case "judge_rejected_terminal":
      return {
        status: "delivered",
        reasoning:
          "abandoned: migrated from judge_rejected_terminal (v0.6 → v0.7); judge retired",
      };
    case "waiting_external":
      return {
        status: "started",
        reasoning:
          "migrated waiting_external → started (v0.6 → v0.7); cooldown machinery retired",
      };
    default:
      return {
        status: "started",
        reasoning: `migrated unrecognised v0.6 status '${v06Status}' → started (v0.7)`,
      };
  }
}

function findCancelReason(events: any[]): string | null {
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (e.kind === "cancel_requested" && typeof e.reason === "string") {
      return e.reason;
    }
    if (e.kind === "status_change" && e.trigger === "cancelled_by_drew") {
      return "drew cancel";
    }
  }
  return null;
}

function isV07Event(line: string): boolean {
  try {
    const obj = JSON.parse(line);
    return (
      obj &&
      typeof obj === "object" &&
      typeof obj.created_by === "string" &&
      typeof obj.status === "string" &&
      ["started", "blocked", "delivered"].includes(obj.status) &&
      typeof obj.timestamp === "string" &&
      typeof obj.contract_id === "string"
    );
  } catch {
    return false;
  }
}

function isV06Record(line: string): boolean {
  try {
    const obj = JSON.parse(line);
    return obj && typeof obj === "object" && obj.kind === "contract";
  } catch {
    return false;
  }
}

interface MigrationResult {
  file: string;
  status: "migrated" | "skipped-already-v07" | "skipped-empty" | "skipped-non-contract";
  contract_id?: string;
  fromStatus?: string;
  toStatus?: string;
}

function migrateFile(path: string): MigrationResult {
  const raw = readFileSync(path, "utf8");
  const lines = raw.split("\n").filter((l) => l.length > 0);
  if (lines.length === 0) {
    return { file: path, status: "skipped-empty" };
  }
  if (isV07Event(lines[0])) {
    return { file: path, status: "skipped-already-v07" };
  }
  if (!isV06Record(lines[0])) {
    return { file: path, status: "skipped-non-contract" };
  }

  const record = JSON.parse(lines[0]);
  const events = lines.slice(1).map((l) => {
    try {
      return JSON.parse(l);
    } catch {
      return null;
    }
  }).filter(Boolean);

  const id = record.id;
  const summary = record.statement ?? "(no summary)";
  const owner = ownerString(record.owner);
  const cancelReason = findCancelReason(events);
  // The v0.6 line-1 record's `status` is only the INITIAL creation status
  // (always "open" in practice). The actual v0.6 final state is the last
  // status_change event's `to` field. Replay events to compute it; fall
  // back to the line-1 status if no status_change events exist.
  let v06FinalStatus = record.status ?? "open";
  let v06ClosedReason = record.closed_on_reason ?? null;
  for (const e of events) {
    if (e.kind === "status_change" && typeof e.to === "string") {
      v06FinalStatus = e.to;
      if (e.to === "satisfied") v06ClosedReason = "satisfied";
      else if (e.to === "judge_rejected_terminal") v06ClosedReason = "judge_rejected_terminal";
    } else if (e.kind === "cancel_acked" && e.decision === "approve") {
      v06FinalStatus = "cancelled";
      v06ClosedReason = "cancelled_by_owner_acked";
    }
  }
  const final = mapFinalState(
    v06FinalStatus,
    v06ClosedReason,
    cancelReason
  );

  const createdAt = record.created_on ?? new Date().toISOString();
  // Bump migrated event timestamp by 1ms so it sorts after creation.
  const migratedAt = new Date(
    new Date(record.updated_on ?? createdAt).getTime() + 1
  ).toISOString();

  const startedEvent: V07Event = {
    timestamp: createdAt,
    contract_id: id,
    status: "started",
    summary,
    reasoning: "migrated from v0.6 contract record (creation event)",
    owner,
    created_by: MIGRATION_CREATED_BY,
    source: "migration:v06-to-v07",
  };

  const finalEvent: V07Event = {
    timestamp: migratedAt,
    contract_id: id,
    status: final.status,
    summary: null,
    reasoning: final.reasoning,
    owner: null,
    created_by: MIGRATION_CREATED_BY,
    source: "migration:v06-to-v07",
  };

  const outLines: string[] = [
    JSON.stringify(startedEvent),
    JSON.stringify(finalEvent),
  ];

  // Atomic rewrite via tmp + rename to avoid torn writes.
  const tmp = `${path}.migrate-tmp`;
  writeFileSync(tmp, outLines.join("\n") + "\n");
  renameSync(tmp, path);

  return {
    file: path,
    status: "migrated",
    contract_id: id,
    fromStatus: record.status,
    toStatus: final.status,
  };
}

export function migrate(dir: string = defaultContractsDir()): MigrationResult[] {
  if (!existsSync(dir)) {
    return [];
  }
  const results: MigrationResult[] = [];
  for (const fname of readdirSync(dir)) {
    if (!fname.endsWith(".jsonl")) continue;
    if (fname.startsWith("_") || fname.startsWith(".")) continue;
    const path = join(dir, fname);
    try {
      results.push(migrateFile(path));
    } catch (e: any) {
      console.error(`[migrate] ${fname}: ${e?.message ?? e}`);
      results.push({ file: path, status: "skipped-non-contract" });
    }
  }
  return results;
}

function summarise(results: MigrationResult[]): void {
  const counts = results.reduce<Record<string, number>>((acc, r) => {
    acc[r.status] = (acc[r.status] ?? 0) + 1;
    return acc;
  }, {});
  console.log("Migration complete:");
  for (const [k, v] of Object.entries(counts)) {
    console.log(`  ${k}: ${v}`);
  }
  for (const r of results) {
    if (r.status === "migrated") {
      console.log(
        `  ${r.contract_id}: ${r.fromStatus} → ${r.toStatus} (${r.file})`
      );
    }
  }
}

if (import.meta.main) {
  const results = migrate();
  summarise(results);
}
