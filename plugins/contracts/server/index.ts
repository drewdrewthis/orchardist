/**
 * Contracts MCP server (stdio transport) — v0.8.
 *
 * Stripped to the canonical spec at references/contracts.md (ADR-011).
 * Three v1 tools (add_contract, update_contract, list_contracts) plus
 * get_contract. No judge, no approval, no cancellation flow.
 *
 * TODO(#630): Rewire add_contract/update_contract to shell out to
 * scripts/contracts/add.sh and scripts/contracts/update.sh (L2 envelope),
 * and list_contracts/get_contract to scripts/contracts/list.sh and
 * scripts/contracts/get.sh (daemon GraphQL reads). Currently the server
 * reads/writes JSONL directly via lib/contract.ts. The script interface
 * and the internal JSONL schema are aligned but the daemon-backed read path
 * (list.sh/get.sh) differs from the local fold used here. Resolve the read
 * path discrepancy before wiring, or add a CONTRACTS_DIR-based fallback.
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import {
  addContract,
  getContract,
  listAllContracts,
  updateContract,
} from "./lib/tools";

const server = new Server(
  {
    name: "contracts",
    version: "0.8.0",
  },
  {
    capabilities: {
      tools: {},
    },
  }
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "add_contract",
      description:
        "File a new contract committing the calling session to a deliverable. " +
        "Logs a `started` event. The owner SHOULD be the caller's session id " +
        "in the form `machine:project:session_id`. summary is the imperative " +
        "of what gets delivered.",
      inputSchema: {
        type: "object",
        properties: {
          summary: {
            type: "string",
            description: "Imperative one-liner of what gets delivered.",
          },
          reasoning: {
            type: "string",
            description: "Why this contract is being filed (audit-trail).",
          },
          owner: {
            type: "string",
            description:
              "Session identifier in `machine:project:session_id` form. Auto-derived from session env when omitted by clients that supply it.",
          },
          source: {
            type: "string",
            description:
              "Optional pointer to the commitment origin (conversation:<uuid>, pr:owner/repo/N, issue:owner/repo/N).",
          },
          created_by: {
            type: "string",
            description: "Freeform agent identifier of the caller.",
          },
        },
        required: ["summary"],
      },
    },
    {
      name: "update_contract",
      description:
        "Append a status event to a contract. status MUST be `started`, `blocked`, " +
        "or `delivered`. To signal abandonment, set status=delivered and prefix " +
        "reasoning with `abandoned:`. owner may be supplied to record a handoff. " +
        "summary is null on updates (fold inherits).",
      inputSchema: {
        type: "object",
        properties: {
          contract_id: { type: "string" },
          status: {
            type: "string",
            enum: ["started", "blocked", "delivered"],
          },
          reasoning: { type: "string" },
          owner: {
            type: "string",
            description:
              "Optional. Setting on an update event records a handoff to a new owner session.",
          },
          source: { type: "string" },
          created_by: { type: "string" },
          summary: {
            type: "string",
            description:
              "Discouraged on updates — set on creation only. The fold inherits.",
          },
        },
        required: ["contract_id", "status", "reasoning"],
      },
    },
    {
      name: "list_contracts",
      description:
        "List contracts matching an optional filter. Returns the folded current " +
        "state of each match (id, summary, status, owner, last reasoning, " +
        "timestamps, full event log).",
      inputSchema: {
        type: "object",
        properties: {
          filter: {
            type: "object",
            properties: {
              status: {
                type: "string",
                enum: ["started", "blocked", "delivered"],
              },
              owner: { type: "string" },
            },
          },
        },
      },
    },
    {
      name: "get_contract",
      description: "Read the folded current state of a single contract.",
      inputSchema: {
        type: "object",
        properties: { id: { type: "string" } },
        required: ["id"],
      },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (req) => {
  const { name, arguments: args } = req.params;
  try {
    let result: unknown;
    switch (name) {
      case "add_contract":
        result = addContract(args as any);
        break;
      case "update_contract":
        result = updateContract(args as any);
        break;
      case "list_contracts":
        result = listAllContracts(args as any);
        break;
      case "get_contract":
        result = getContract((args as any).id);
        break;
      default:
        throw new Error(`Unknown tool: ${name}`);
    }
    return {
      content: [{ type: "text", text: JSON.stringify(result, null, 2) }],
    };
  } catch (err: any) {
    return {
      isError: true,
      content: [
        { type: "text", text: `Error: ${err?.message ?? String(err)}` },
      ],
    };
  }
});

const transport = new StdioServerTransport();
await server.connect(transport);
process.stderr.write("[contracts-mcp] v0.8 ready\n");
