/**
 * Houdini config for the orchard daemon. SPA mode — `static: true` strips
 * the SSR session plumbing because `+layout.ts` exports `ssr=false` and
 * the build target is `adapter-static` with an index.html fallback.
 *
 * `defaultCachePolicy: CacheAndNetwork` is the point of the migration —
 * components see the cache instantly and the cache patches in network
 * deltas as subscriptions fire. Subscriptions auto-bind by `__typename:id`
 * because the daemon already ships idiomatic Node/ID conventions.
 *
 * `watchSchema.url` polls the live daemon at codegen time, so the daemon
 * MUST be reachable on `127.0.0.1:7777` when running `pnpm dev` /
 * `pnpm tauri dev`. We don't snapshot the schema — drift would be silent.
 */

/** @type {import('houdini').ConfigFile} */
const config = {
	watchSchema: {
		url: "http://127.0.0.1:7777/graphql",
	},
	defaultCachePolicy: "CacheAndNetwork",
	plugins: {
		"houdini-svelte": {
			client: "./src/client.ts",
			static: true,
		},
	},
};

export default config;
