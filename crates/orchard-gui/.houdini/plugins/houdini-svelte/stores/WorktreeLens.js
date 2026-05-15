import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/WorktreeLens'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class WorktreeLensStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "WorktreeLensStore",
			variables: false,
		})
	}
}

export async function load_WorktreeLens(params) {
  await initClient()

	const store = new WorktreeLensStore()

	await store.fetch(params)

	return {
		WorktreeLens: store,
	}
}
