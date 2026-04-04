import scenario from "@langwatch/scenario";
import fs from "fs";
import { execSync } from "child_process";
import { describe, it, expect, afterAll } from "vitest";
import dotenv from "dotenv";
import os from "os";
import path from "path";
import { fileURLToPath } from "url";
import { openai } from "@ai-sdk/openai";
import {
  createClaudeCodeAgent,
  toolCallFix,
  assertSkillWasRead,
} from "./helpers/claude-code-adapter";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

dotenv.config({ path: path.resolve(__dirname, "../../.env") });

const isCI = !!process.env.CI;

const judgeModel = openai("gpt-5-mini");

function setupWorkDir(tempFolder: string) {
  // Initialize a git repo so Claude Code can operate
  execSync("git init", { cwd: tempFolder });
  execSync("git commit --allow-empty -m 'init'", { cwd: tempFolder });

  // Copy the install-orchard skill into .skills/
  const skillDir = path.join(tempFolder, ".skills", "install-orchard");
  fs.mkdirSync(skillDir, { recursive: true });
  fs.copyFileSync(
    path.resolve(__dirname, "../../SKILL.md"),
    path.join(skillDir, "SKILL.md")
  );

  // Create a CLAUDE.md that points to the skill
  fs.writeFileSync(
    path.join(tempFolder, "CLAUDE.md"),
    "Read and follow the instructions in .skills/install-orchard/SKILL.md before doing anything else.\n"
  );
}

describe("Install Orchard Skill", () => {
  const tempFolders: string[] = [];

  afterAll(() => {
    for (const folder of tempFolders) {
      fs.rmSync(folder, { recursive: true, force: true });
    }
  });

  it.skipIf(isCI)(
    "guides a new user through orchard setup",
    async () => {
      const tempFolder = fs.mkdtempSync(
        path.join(os.tmpdir(), "orchard-install-skill-")
      );
      tempFolders.push(tempFolder);

      setupWorkDir(tempFolder);

      const result = await scenario.run({
        name: "New user installs Orchard",
        description:
          "A new user invokes the install-orchard skill and is guided through prerequisites, build, and configuration.",
        agents: [
          createClaudeCodeAgent({ workingDirectory: tempFolder }),
          scenario.userSimulatorAgent({ model: judgeModel }),
          scenario.judgeAgent({
            model: judgeModel,
            criteria: [
              "Agent should check for prerequisites (git, tmux, gh, cargo)",
              "Agent should ask about tmux familiarity",
              "Agent should explain how to clone and build Orchard",
              "Agent should guide the user through repo configuration",
            ],
          }),
        ],
        script: [
          scenario.user("install orchard"),
          scenario.agent(),
          (state) => {
            toolCallFix(state);
            assertSkillWasRead(state, "install-orchard");
          },
          scenario.judge(),
        ],
      });

      expect(result.success).toBe(true);
    },
    600_000
  );
});
