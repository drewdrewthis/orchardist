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

      // Initialize a git repo so Claude Code can operate
      execSync("git init", { cwd: tempFolder });
      execSync("git commit --allow-empty -m 'init'", { cwd: tempFolder });

      const result = await scenario.run({
        name: "New user installs Orchard",
        description:
          "A new user invokes the install-orchard skill and is guided through " +
          "prerequisites, cloning the git-orchard-rs repository, building from " +
          "source, configuring a repo, and optionally setting up Telegram.",
        agents: [
          createClaudeCodeAgent({
            workingDirectory: tempFolder,
            skillPath: path.resolve(__dirname, "../../SKILL.md"),
          }),
          scenario.userSimulatorAgent({ model: judgeModel }),
          scenario.judgeAgent({
            model: judgeModel,
            criteria: [
              "Agent checks for prerequisites (git, tmux, gh, cargo) by running version commands",
              "Agent asks about tmux familiarity level and adapts explanation accordingly",
              "Agent instructs the user to clone from https://github.com/drewdrewthis/git-orchard-rs.git and build with cargo build --release",
              "Agent explains how to add the orchard binary to PATH (symlink or cargo install)",
              "Agent guides configuration of a repo with .orchard.json",
              "Agent mentions Telegram setup for orchardist notifications",
              "Agent explains how to resume sessions with --continue flag",
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
          // The install skill is multi-turn (asks questions), let it run
          scenario.proceed(6),
          scenario.judge(),
        ],
      });

      expect(result.success).toBe(true);
    },
    600_000
  );
});
