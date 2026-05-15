import { QueryStore } from '../runtime/stores/query'
import artifact from '$houdini/artifacts/OpenPanel'
import { initClient } from '$houdini/plugins/houdini-svelte/runtime/client'

export class OpenPanelStore extends QueryStore {
	constructor() {
		super({
			artifact,
			storeName: "OpenPanelStore",
			variables: false,
		})
	}
}

export async function load_OpenPanel(params) {
  await initClient()

	const store = new OpenPanelStore()

	await store.fetch(params)

	return {
		OpenPanel: store,
	}
}
