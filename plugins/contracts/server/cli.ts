#!/usr/bin/env bun
/**
 * Hook helper CLI for v0.7. Bash hook scripts shell out here for the simple
 * "list open contracts owned by this session" lookup the Stop /
 * UserPromptSubmit / SessionStart hooks need.
 *
 * Subcommands:
 *   list-mine --owner <owner>
 *       List the calling session's open contracts (status != delivered).
 *       Outputs one row per contract: `- <id> [<status>] <summary>`.
 *   inject-prompt-context --owner <owner>
 *       Build the system-reminder block for UserPromptSubmit / SessionStart.
 *       Outputs a multi-section string ready for jq to wrap.
 *
 * Exit codes:
 *   0 — success, output on stdout
 *   1 — invalid args
 *   2 — internal error
 */

import { ContractStatus, FoldedContract, listContracts } from "./lib/contract";

function arg(name: string): string | null {
  const i = process.argv.indexOf(`--${name}`);
  if (i < 0 || i + 1 >= process.argv.length) return null;
  return process.argv[i + 1];
}

const subcmd = process.argv[2];

if (!subcmd) {
  console.error("usage: cli.ts <subcommand> [args...]");
  process.exit(1);
}

/**
 * List open contracts whose owner field matches the supplied selector.
 *
 * The hook scripts pass the calling session's `machine:project:session_id`
 * triple, but contract authors may have used any owner shape (the spec
 * permits any non-null string). To keep the Stop / UserPromptSubmit
 * reminder useful across owner-string conventions, the match is:
 *   - exact owner equality, OR
 *   - the owner field CONTAINS the supplied selector as a substring.
 *
 * The latter handles selectors that pass just a session_id (no machine /
 * project prefix) and contract owners that use the spec's full triple.
 */
function listOpenForOwner(selector: string): FoldedContract[] {
  return listContracts({}).filter((c) => {
    if (c.status === "delivered") return false;
    if (!c.owner) return false;
    return c.owner === selector || c.owner.includes(selector);
  });
}

function formatRow(c: FoldedContract): string {
  return `- ${c.contract_id} [${c.status}] ${c.summary} — ${c.reasoning}`;
}

try {
  switch (subcmd) {
    case "list-mine": {
      const owner = arg("owner");
      if (!owner) {
        console.error("--owner required");
        process.exit(1);
      }
      const open = listOpenForOwner(owner);
      console.log(open.map(formatRow).join("\n"));
      break;
    }

    case "inject-prompt-context": {
      const owner = arg("owner");
      if (!owner) {
        console.error("--owner required");
        process.exit(1);
      }
      const open = listOpenForOwner(owner);
      if (open.length === 0) {
        // Empty output → bash hook skips emitting an additionalContext block.
        break;
      }
      const blocks: string[] = [];
      const startedOrBlocked = open.filter(
        (c) => c.status === "started" || c.status === "blocked"
      );
      if (startedOrBlocked.length > 0) {
        const lines = ["[OPEN CONTRACTS — yours, not yet delivered]"];
        for (const c of startedOrBlocked) lines.push(formatRow(c));
        lines.push(
          "These commitments are still on you. Drive each to a delivered event " +
            "(or a delivered event with reasoning prefixed `abandoned:` if no longer relevant)."
        );
        blocks.push(lines.join("\n"));
      }
      console.log(blocks.join("\n\n"));
      break;
    }

    default:
      console.error(`unknown subcommand: ${subcmd}`);
      process.exit(1);
  }
} catch (e: any) {
  process.stderr.write(`[cli] error: ${e?.message ?? String(e)}\n`);
  process.exit(2);
}
