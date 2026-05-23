/**
 * MCP tool implementations for v0.7. Pure functions over the JSONL store.
 *
 * Three v1 tools per references/contracts.md:
 *   - add_contract({summary, reasoning?, owner?, source?})
 *       Logs a `started` event. Returns the new contract_id.
 *   - update_contract({contract_id, status, reasoning, owner?, source?})
 *       Logs a status event. Status must be `started | blocked | delivered`.
 *   - list_contracts({filter?: {status?, owner?}})
 *       Returns folded current state of matching contracts.
 *
 * Plus get_contract({id}) as a convenience read.
 */

import {
  ContractEvent,
  ContractStatus,
  FoldedContract,
  ListFilter,
  appendEvent,
  listContracts,
  newContractId,
  nowIso,
  readContract,
} from "./contract";

// ---- add_contract ---------------------------------------------------------

export interface AddContractInput {
  summary: string;
  reasoning?: string;
  owner?: string;
  source?: string;
  created_by?: string;
}

export function addContract(input: AddContractInput): {
  contract_id: string;
} {
  if (!input.summary || input.summary.trim().length === 0) {
    throw new Error("summary is required");
  }
  const id = newContractId();
  const event: ContractEvent = {
    timestamp: nowIso(),
    contract_id: id,
    status: "started",
    summary: input.summary.trim(),
    reasoning: (input.reasoning ?? "contract filed").trim() || "contract filed",
    owner: input.owner ?? null,
    created_by: input.created_by ?? input.owner ?? "unknown",
    source: input.source ?? null,
  };
  appendEvent(event);
  return { contract_id: id };
}

// ---- update_contract -------------------------------------------------------

export interface UpdateContractInput {
  contract_id: string;
  status: ContractStatus;
  reasoning: string;
  owner?: string;
  source?: string;
  created_by?: string;
  summary?: string;
}

export function updateContract(input: UpdateContractInput): {
  contract_id: string;
  status: ContractStatus;
} {
  if (!input.reasoning || input.reasoning.trim().length === 0) {
    throw new Error("reasoning is required on every update");
  }
  if (
    input.status !== "started" &&
    input.status !== "blocked" &&
    input.status !== "delivered"
  ) {
    throw new Error(
      `status must be started|blocked|delivered, got ${input.status}`
    );
  }
  // Verify the contract exists; readContract throws if it doesn't.
  const existing = readContract(input.contract_id);
  const event: ContractEvent = {
    timestamp: nowIso(),
    contract_id: input.contract_id,
    status: input.status,
    summary: input.summary ?? null,
    reasoning: input.reasoning.trim(),
    owner: input.owner ?? null,
    created_by: input.created_by ?? input.owner ?? existing.created_by,
    source: input.source ?? null,
  };
  appendEvent(event);
  return { contract_id: input.contract_id, status: input.status };
}

// ---- list_contracts -------------------------------------------------------

export interface ListContractsInput {
  filter?: ListFilter;
}

export function listAllContracts(
  input: ListContractsInput = {}
): FoldedContract[] {
  return listContracts(input.filter ?? {});
}

// ---- get_contract ---------------------------------------------------------

export function getContract(id: string): FoldedContract {
  return readContract(id);
}
