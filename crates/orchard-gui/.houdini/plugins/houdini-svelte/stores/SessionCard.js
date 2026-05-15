import { FragmentStore } from '../runtime/stores/fragment'
import artifact from '$houdini/artifacts/SessionCard'


// create the fragment store

export class SessionCardStore extends FragmentStore {
	constructor() {
		super({
			artifact,
			storeName: "SessionCardStore",
			variables: true,
			
		})
	}
}
