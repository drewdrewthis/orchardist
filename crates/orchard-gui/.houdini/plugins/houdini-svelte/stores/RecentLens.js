import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/RecentLens'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class RecentLensStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "RecentLensStore",
			variables: false,
		})
	}
}

export async function load_RecentLens(params) {
  await initClient()

	const store = new RecentLensStore()

	await store.fetch(params)

	return {
		RecentLens: store,
	}
}
