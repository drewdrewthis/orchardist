/**
 * E2E test for the contracts plugin (v0.7).
 *
 * Exercises the v0.7 surface end-to-end:
 *
 *   - add_contract → started event written, contract readable
 *   - update_contract status=blocked → reasoning recorded
 *   - update_contract status=delivered → terminal event recorded
 *   - update_contract status=delivered with `abandoned:` reasoning → still terminal
 *   - list_contracts filters by status + owner
 *   - Migration script idempotence on a synthesised v0.6 file
 *
 * Each test gets its own CONTRACTS_DIR via mkdtempSync so they don't
 * leak state.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";

const TMP = mkdtempSync(join(tmpdir(), "contracts-e2e-v07-"));
process.env.CONTRACTS_DIR = TMP;

// Late-import so the env var takes effect.
const tools = await import("../server/lib/tools");
const contract = await import("../server/lib/contract");

describe("v0.7 — add_contract + update_contract + list_contracts", () => {
  test("add_contract logs a started event and returns a contract id", () => {
    const r = tools.addContract({
      summary: "exercise the v0.7 surface",
      reasoning: "test setup",
      owner: "local:claude:sess-aaa",
      created_by: "test",
    });
    expect(r.contract_id).toMatch(/^C-\d{4}-\d{2}-\d{2}-[a-f0-9]{8}$/);

    const folded = tools.getContract(r.contract_id);
    expect(folded.status).toBe("started");
    expect(folded.summary).toBe("exercise the v0.7 surface");
    expect(folded.owner).toBe("local:claude:sess-aaa");
    expect(folded.events).toHaveLength(1);
    expect(folded.events[0].status).toBe("started");
    expect(folded.events[0].created_by).toBe("test");
  });

  test("add_contract refuses missing summary", () => {
    expect(() =>
      tools.addContract({ summary: "", owner: "x", created_by: "test" })
    ).toThrow(/summary is required/);
    expect(() =>
      tools.addContract({ summary: "   ", owner: "x", created_by: "test" })
    ).toThrow(/summary is required/);
  });

  test("update_contract appends events and the fold tracks status", () => {
    const r = tools.addContract({
      summary: "track status transitions",
      owner: "local:claude:sess-bbb",
      created_by: "test",
    });
    const id = r.contract_id;

    tools.updateContract({
      contract_id: id,
      status: "blocked",
      reasoning: "waiting on CI",
      created_by: "test",
    });
    expect(tools.getContract(id).status).toBe("blocked");
    expect(tools.getContract(id).reasoning).toBe("waiting on CI");

    tools.updateContract({
      contract_id: id,
      status: "started",
      reasoning: "CI green, resuming",
      created_by: "test",
    });
    expect(tools.getContract(id).status).toBe("started");

    tools.updateContract({
      contract_id: id,
      status: "delivered",
      reasoning: "shipped at PR #42",
      created_by: "test",
    });
    const finalFolded = tools.getContract(id);
    expect(finalFolded.status).toBe("delivered");
    expect(finalFolded.reasoning).toBe("shipped at PR #42");
    expect(finalFolded.events).toHaveLength(4);
  });

  test("update_contract enforces canonical status enum", () => {
    const r = tools.addContract({
      summary: "enum gate",
      owner: "local:claude:sess-ccc",
      created_by: "test",
    });
    expect(() =>
      tools.updateContract({
        contract_id: r.contract_id,
        // @ts-expect-error — testing runtime enforcement
        status: "satisfied",
        reasoning: "x",
      })
    ).toThrow(/status must be started\|blocked\|delivered/);
  });

  test("update_contract requires reasoning", () => {
    const r = tools.addContract({
      summary: "reasoning required",
      owner: "local:claude:sess-ddd",
      created_by: "test",
    });
    expect(() =>
      tools.updateContract({
        contract_id: r.contract_id,
        status: "started",
        reasoning: "",
      })
    ).toThrow(/reasoning is required/);
    expect(() =>
      tools.updateContract({
        contract_id: r.contract_id,
        status: "started",
        reasoning: "   ",
      })
    ).toThrow(/reasoning is required/);
  });

  test("update_contract refuses unknown contract id", () => {
    expect(() =>
      tools.updateContract({
        contract_id: "C-9999-99-99-deadbeef",
        status: "delivered",
        reasoning: "ghost",
      })
    ).toThrow(/not found/);
  });

  test("delivered with `abandoned:` prefix is still terminal", () => {
    const r = tools.addContract({
      summary: "exercise abandonment",
      owner: "local:claude:sess-eee",
      created_by: "test",
    });
    tools.updateContract({
      contract_id: r.contract_id,
      status: "delivered",
      reasoning: "abandoned: scope shifted, dropping",
      created_by: "test",
    });
    const folded = tools.getContract(r.contract_id);
    expect(folded.status).toBe("delivered");
    expect(folded.reasoning).toMatch(/^abandoned:/);
  });

  test("list_contracts filters by status and owner", () => {
    const a = tools.addContract({
      summary: "filter A",
      owner: "local:claude:filter-owner-A",
      created_by: "test",
    });
    const b = tools.addContract({
      summary: "filter B",
      owner: "local:claude:filter-owner-B",
      created_by: "test",
    });
    tools.updateContract({
      contract_id: b.contract_id,
      status: "delivered",
      reasoning: "B done",
      created_by: "test",
    });

    const startedOnly = tools.listAllContracts({
      filter: { status: "started" },
    });
    const startedIds = startedOnly.map((c) => c.contract_id);
    expect(startedIds).toContain(a.contract_id);
    expect(startedIds).not.toContain(b.contract_id);

    const ownerB = tools.listAllContracts({
      filter: { owner: "local:claude:filter-owner-B" },
    });
    const ownerBIds = ownerB.map((c) => c.contract_id);
    expect(ownerBIds).toEqual([b.contract_id]);
  });

  test("first-event summary is preserved across update events that pass null", () => {
    const r = tools.addContract({
      summary: "summary inheritance",
      owner: "local:claude:sess-summary",
      created_by: "test",
    });
    tools.updateContract({
      contract_id: r.contract_id,
      status: "blocked",
      reasoning: "no summary on this update",
      created_by: "test",
    });
    expect(tools.getContract(r.contract_id).summary).toBe(
      "summary inheritance"
    );
  });

  test("owner handoff via update_contract is reflected in the fold", () => {
    const r = tools.addContract({
      summary: "owner handoff",
      owner: "local:claude:initial-owner",
      created_by: "test",
    });
    expect(tools.getContract(r.contract_id).owner).toBe(
      "local:claude:initial-owner"
    );
    tools.updateContract({
      contract_id: r.contract_id,
      status: "started",
      owner: "local:claude:new-owner",
      reasoning: "handoff to new session",
      created_by: "new-owner",
    });
    expect(tools.getContract(r.contract_id).owner).toBe(
      "local:claude:new-owner"
    );
  });
});

describe("v0.7 — migration from v0.6 jsonl shapes is idempotent", () => {
  // Each migration test isolates its own dir so we don't pollute the
  // outer suite's CONTRACTS_DIR.
  const MIG_TMP = mkdtempSync(join(tmpdir(), "contracts-migrate-"));
  const ORIGINAL_DIR = process.env.CONTRACTS_DIR!;

  beforeAll(() => {
    process.env.CONTRACTS_DIR = MIG_TMP;
  });
  afterAll(() => {
    rmSync(MIG_TMP, { recursive: true, force: true });
    process.env.CONTRACTS_DIR = ORIGINAL_DIR;
  });

  function writeV06Stuck(id: string, status: string, slug: string) {
    const path = join(MIG_TMP, `${id}--${slug}.jsonl`);
    const record = {
      kind: "contract",
      id,
      statement: `legacy ${status} contract`,
      owner: {
        session_id: "legacy-sess",
        agent_name: "legacy-agent",
        vm_address: "legacy-vm",
      },
      reports_to: { kind: "drew", agent_name: null, vm_address: null },
      parent_contract_id: null,
      child_contract_ids: [],
      created_on: "2026-04-01T00:00:00.000Z",
      updated_on: "2026-04-01T01:00:00.000Z",
      closed_on: null,
      closed_on_reason: null,
      status,
      judge_verdict: null,
      evidence: null,
      cooldown_until: null,
    };
    writeFileSync(path, JSON.stringify(record) + "\n");
    return path;
  }

  let migrate: (dir?: string) => unknown;
  beforeAll(async () => {
    const mod = await import("../scripts/migrate-v06-to-v07");
    migrate = mod.migrate;
  });
  async function runMigration(): Promise<void> {
    migrate(MIG_TMP);
  }

  test("pending_drew_approval folds to delivered (judges had passed)", async () => {
    const path = writeV06Stuck(
      "C-2026-04-01-aaaa1111",
      "pending_drew_approval",
      "pdas"
    );
    await runMigration();
    const folded = contract.readContract("C-2026-04-01-aaaa1111");
    expect(folded.status).toBe("delivered");
    expect(folded.reasoning).toMatch(/judges had passed/);
    // Owner reconstructed as machine:project:session_id
    expect(folded.owner).toBe("legacy-vm:claude:legacy-sess");
    // Two events: started (creation) + delivered (migration final)
    expect(folded.events).toHaveLength(2);
  });

  test("awaiting_cancel_ack folds to delivered with abandoned: prefix", async () => {
    writeV06Stuck("C-2026-04-01-bbbb2222", "awaiting_cancel_ack", "aca");
    await runMigration();
    const folded = contract.readContract("C-2026-04-01-bbbb2222");
    expect(folded.status).toBe("delivered");
    expect(folded.reasoning).toMatch(/^abandoned:/);
  });

  test("satisfied folds to delivered (judges had passed)", async () => {
    writeV06Stuck("C-2026-04-01-cccc3333", "satisfied", "sat");
    await runMigration();
    const folded = contract.readContract("C-2026-04-01-cccc3333");
    expect(folded.status).toBe("delivered");
    expect(folded.reasoning).toMatch(/judges had passed/);
  });

  test("cancelled folds to delivered with abandoned: prefix", async () => {
    writeV06Stuck("C-2026-04-01-dddd4444", "cancelled", "can");
    await runMigration();
    const folded = contract.readContract("C-2026-04-01-dddd4444");
    expect(folded.status).toBe("delivered");
    expect(folded.reasoning).toMatch(/^abandoned:/);
  });

  test("open folds to started losslessly", async () => {
    writeV06Stuck("C-2026-04-01-eeee5555", "open", "open");
    await runMigration();
    const folded = contract.readContract("C-2026-04-01-eeee5555");
    expect(folded.status).toBe("started");
    expect(folded.reasoning).toMatch(/migrated/);
  });

  test("re-running migration is a no-op on already-migrated files", async () => {
    writeV06Stuck("C-2026-04-01-ffff6666", "satisfied", "double");
    await runMigration();
    const path = join(MIG_TMP, "C-2026-04-01-ffff6666--double.jsonl");
    const firstPass = readFileSync(path, "utf8");
    await runMigration();
    const secondPass = readFileSync(path, "utf8");
    expect(secondPass).toBe(firstPass);
  });
});

afterAll(() => {
  rmSync(TMP, { recursive: true, force: true });
});
