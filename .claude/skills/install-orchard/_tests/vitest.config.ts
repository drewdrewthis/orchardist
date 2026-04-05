import { defineConfig } from "vitest/config";
import { withScenario } from "@langwatch/scenario/integrations/vitest/config";

export default withScenario(
  defineConfig({
    test: {
      testTimeout: 60 * 60 * 1000, // 1 hour
    },
  })
);
