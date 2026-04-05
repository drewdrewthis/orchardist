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
  assertSkillWasRead,
} from "./helpers/claude-code-adapter";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

dotenv.config({ path: path.resolve(__dirname, "../.env") });

const judgeModel = openai("gpt-5-mini");
const userSimModel = openai("gpt-4o-nano");

const CRITERIA = [
  "Agent checked for git and installed it if missing",
  "Agent checked for tmux and installed it if missing",
  "Agent checked for gh (GitHub CLI) and installed it if missing",
  "Agent checked for node and installed it if missing",
  "Agent asked the user about their tmux familiarity and explained tmux basics (sessions, windows, panes) and how Orchard uses tmux",
  "Agent installed orchard (via npm install -g git-orchard, or fell back to cargo build --release when npm failed)",
  "Agent ran orchard --help to verify the installation",
  "Agent configured the repo with .orchard.json",
  "Agent asked the user if they want Telegram notifications and explained what it provides",
  "Agent explained the Telegram setup steps (BotFather, plugin install, configure, pair)",
  "Agent explained how to resume sessions with --continue or mentioned the orchardist session concept",
  "Agent set up the orchardist session",
];

describe("Install Orchard Skill", () => {
  const tempFolders: string[] = [];

  afterAll(() => {
    for (const folder of tempFolders) {
      fs.rmSync(folder, { recursive: true, force: true });
    }
  });

  it(
    "guides a new user through orchard setup",
    async () => {
      const tempFolder = fs.mkdtempSync(
        path.join(os.tmpdir(), "orchard-install-skill-")
      );
      tempFolders.push(tempFolder);

      // Initialize a git repo on a non-main branch to avoid guard-main-branch hook
      execSync("git init", { cwd: tempFolder });
      execSync("git checkout -b setup", { cwd: tempFolder });
      execSync("git commit --allow-empty -m 'init'", { cwd: tempFolder });

      const result = await scenario.run({
        name: "New user installs Orchard",
        description:
          "A new user who is not familiar with tmux invokes /install-orchard. " +
          "They want the full setup: orchard installed, repo configured, " +
          "Telegram notifications, and an orchardist session.",
        agents: [
          createClaudeCodeAgent({
            workingDirectory: tempFolder,
            skillPath: path.resolve(__dirname, "../SKILL.md"),
            model: "haiku",
          }),
          scenario.userSimulatorAgent({ model: userSimModel }),
          scenario.judgeAgent({ model: judgeModel, criteria: CRITERIA }),
        ],
        script: [
          scenario.user("/install-orchard"),
          scenario.agent(),
          (state) => {
            assertSkillWasRead(state, "install-orchard");
          },
          scenario.proceed(8),
          () => {
            // Verify orchard binary is installed and runnable
            const helpOutput = execSync("orchard --help 2>&1", {
              cwd: tempFolder,
              encoding: "utf8",
            });
            expect(helpOutput).toContain("orchard");

            // Verify repo was configured
            expect(
              fs.existsSync(path.join(tempFolder, ".orchard.json")),
              "Expected .orchard.json to be created in the repo"
            ).toBe(true);
          },
          scenario.judge(),
        ],
      });

      console.log("Scenario result:", JSON.stringify(result, null, 2));
      expect(result.success).toBe(true);
    },
    600_000
  );
});
