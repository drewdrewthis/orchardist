import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/TmuxLens'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class TmuxLensStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "TmuxLensStore",
			variables: false,
		})
	}
}

export async function load_TmuxLens(params) {
  await initClient()

	const store = new TmuxLensStore()

	await store.fetch(params)

	return {
		TmuxLens: store,
	}
}
