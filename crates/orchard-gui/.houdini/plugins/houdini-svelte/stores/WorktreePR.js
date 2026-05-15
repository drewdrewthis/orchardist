import { FragmentStore } from '../runtime/stores/fragment'
import artifact from '$houdini/artifacts/WorktreePR'


// create the fragment store

export class WorktreePRStore extends FragmentStore {
	constructor() {
		super({
			artifact,
			storeName: "WorktreePRStore",
			variables: true,
			
		})
	}
}
