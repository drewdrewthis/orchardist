import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/WorktreesList'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class WorktreesListStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "WorktreesListStore",
			variables: false,
		})
	}
}

export async function load_WorktreesList(params) {
  await initClient()

	const store = new WorktreesListStore()

	await store.fetch(params)

	return {
		WorktreesList: store,
	}
}
