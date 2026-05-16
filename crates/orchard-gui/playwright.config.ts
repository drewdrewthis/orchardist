import { defineConfig } from "playwright/test";

// iPhone 14 viewport dimensions.
const iphone14 = {
	viewport: { width: 390, height: 844 },
	userAgent:
		"Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1",
	isMobile: true,
	hasTouch: true,
};

export default defineConfig({
	testDir: "./tests",
	testMatch: "**/*.spec.ts",
	use: {
		baseURL: "http://localhost:1420",
	},
	projects: [
		{
			name: "iPhone 14",
			use: iphone14,
		},
	],
});
