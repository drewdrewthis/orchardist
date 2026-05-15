import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/IssueLens'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class IssueLensStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "IssueLensStore",
			variables: false,
		})
	}
}

export async function load_IssueLens(params) {
  await initClient()

	const store = new IssueLensStore()

	await store.fetch(params)

	return {
		IssueLens: store,
	}
}
