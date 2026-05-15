import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/HostsList'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class HostsListStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "HostsListStore",
			variables: false,
		})
	}
}

export async function load_HostsList(params) {
  await initClient()

	const store = new HostsListStore()

	await store.fetch(params)

	return {
		HostsList: store,
	}
}
