import { FragmentStore } from '../runtime/stores/fragment'
import artifact from '$houdini/artifacts/PaneCard'


// create the fragment store

export class PaneCardStore extends FragmentStore {
	constructor() {
		super({
			artifact,
			storeName: "PaneCardStore",
			variables: true,
			
		})
	}
}
