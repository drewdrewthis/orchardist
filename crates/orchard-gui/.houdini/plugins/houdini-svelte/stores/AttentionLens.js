import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/AttentionLens'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class AttentionLensStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "AttentionLensStore",
			variables: false,
		})
	}
}

export async function load_AttentionLens(params) {
  await initClient()

	const store = new AttentionLensStore()

	await store.fetch(params)

	return {
		AttentionLens: store,
	}
}
