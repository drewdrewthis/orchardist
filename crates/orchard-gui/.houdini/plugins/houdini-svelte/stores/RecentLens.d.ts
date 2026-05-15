import type { RecentLens$input, RecentLens$result, QueryStore, QueryStoreFetchParams} from '$houdini'

export declare class RecentLensStore extends QueryStore<RecentLens$result, RecentLens$input> {
	/**
	 * ### Route Loads
	 * In a route's load function, manually instantiating a store can be used to look at the result:
	 *
	 * ```js
	 * export async function load(event) {
	 * 	const store = new RecentLensStoreStore()
	 * 	const { data } = await store.fetch({event})
	 *  console.log('do something with', data)
	 *
	 * 	return {
	 * 		RecentLensStore: store,
	 * 	}
	 * }
	 *
	 * ```
	 *
	 * ### Client Side Loading
	 * When performing a client-side only fetch, the best practice to use a store _manually_ is to do the following:
	 *
	 * ```js
	 * const store = new RecentLensStoreStore()
	 *
	 * $: browser && store.fetch({ variables });
	 * ```
	 */
	constructor() {
		// @ts-ignore
		super({})
	}
}

/**
 * ### Manual Loads
 * Usually your load function will look like this:
 *
 * ```js
 * import { load_RecentLens } from '$houdini';
 * import type { PageLoad } from './$types';
 *
 * export const load: PageLoad = async (event) => {
 *   const variables = {
 *     id: // Something like: event.url.searchParams.get('id')
 *   };
 *
 *   return await load_RecentLens({ event, variables });
 * };
 * ```
 *
 * ### Multiple stores to load
 * You can trigger them in parallel with `loadAll` function
 *
 * ```js
 * import { loadAll, load_RecentLens } from '$houdini';
 * import type { PageLoad } from './$types';
 *
 * export const load: PageLoad = async (event) => {
 *   const variables = {
 *     id: // Something like: event.url.searchParams.get('id')
 *   };
 *
 *   return await await loadAll(
 *     load_RecentLens({ event, variables }),
 *     // load_ANOTHER_STORE
 *   );
 * };
 * ```
 */
export declare const load_RecentLens: (params: QueryStoreFetchParams<RecentLens$result, RecentLens$input>) => Promise<{RecentLens: RecentLensStore}>
