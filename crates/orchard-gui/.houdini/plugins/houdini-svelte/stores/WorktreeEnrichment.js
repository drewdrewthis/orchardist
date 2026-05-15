import { FragmentStore } from '../runtime/stores/fragment'
import artifact from '$houdini/artifacts/WorktreeEnrichment'


// create the fragment store

export class WorktreeEnrichmentStore extends FragmentStore {
	constructor() {
		super({
			artifact,
			storeName: "WorktreeEnrichmentStore",
			variables: true,
			
		})
	}
}
