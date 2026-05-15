import { getCurrentConfig } from "$houdini/runtime/lib/config";
import * as log from "$houdini/runtime/lib/log";
import { extractPageInfo } from "$houdini/runtime/lib/pageInfo";
import { cursorHandlers, offsetHandlers } from "$houdini/runtime/lib/pagination";
import { CompiledQueryKind, CachePolicy, ArtifactKind } from "$houdini/runtime/lib/types";
import { get, derived } from "svelte/store";
import { clientStarted, isBrowser } from "../adapter";
import { getClient, initClient } from "../client";
import { getSession } from "../session";
import { BaseStore } from "./base";
class QueryStore extends BaseStore {
  variables;
  kind = CompiledQueryKind;
  loadPending = false;
  storeName;
  constructor({ artifact, storeName, variables }) {
    const fetching = artifact.pluginData["houdini-svelte"]?.isManualLoad !== true;
    super({
      artifact,
      fetching,
      initialize: !artifact.pluginData["houdini-svelte"].isManualLoad
    });
    this.storeName = storeName;
    this.variables = variables;
  }
  async fetch(args) {
    const client = await initClient();
    this.setup(false);
    const { policy, params, context } = await fetchParams(this.artifact, this.storeName, args);
    if (!isBrowser && !(params && "fetch" in params) && (!params || !("event" in params))) {
      log.error(contextError(this.storeName));
      throw new Error("Error, check above logs for help.");
    }
    const isLoadFetch = Boolean("event" in params && params.event);
    const isComponentFetch = !isLoadFetch;
    if (this.loadPending && isComponentFetch) {
      log.error(`\u26A0\uFE0F Encountered fetch from your component while ${this.storeName}.load was running.
This will result in duplicate queries. If you are trying to ensure there is always a good value, please a CachePolicy instead.`);
      return get(this.observer);
    }
    if (isComponentFetch) {
      params.blocking = true;
    }
    const config = getCurrentConfig();
    const config_svelte = config.plugins["houdini-svelte"];
    const pluginArtifact = this.artifact.pluginData["houdini-svelte"];
    let need_to_block = false;
    if (client.throwOnError_operations.includes("all") || client.throwOnError_operations.includes("query")) {
      if (config_svelte.defaultRouteBlocking === false) {
        log.info(
          '[Houdini] \u26A0\uFE0F throwOnError with operation "all" or "query", is not compatible with defaultRouteBlocking set to "false"'
        );
      }
    }
    if (config_svelte.defaultRouteBlocking === true) {
      need_to_block = true;
    }
    if (client.throwOnError_operations.includes("all") || client.throwOnError_operations.includes("query")) {
      need_to_block = true;
    }
    if (pluginArtifact?.set_blocking === true) {
      need_to_block = true;
    } else if (pluginArtifact?.set_blocking === false) {
      need_to_block = false;
    }
    if (params?.blocking === true) {
      need_to_block = true;
    } else if (params?.blocking === false) {
      need_to_block = false;
    }
    if (isLoadFetch) {
      this.loadPending = true;
    }
    if (isBrowser && this.artifact.enableLoadingState) {
      need_to_block = false;
    }
    const fakeAwait = clientStarted && isBrowser && !need_to_block;
    const usedVariables = {
      ...this.artifact.input?.defaults,
      ...params.variables
    };
    const refersToCache = policy !== CachePolicy.NetworkOnly && policy !== CachePolicy.NoCache;
    if (refersToCache && fakeAwait) {
      await this.observer.send({
        fetch: context.fetch,
        variables: usedVariables,
        metadata: params.metadata,
        session: context.session,
        policy: CachePolicy.CacheOnly,
        silenceEcho: true,
        abortController: params.abortController
      });
    }
    const request = this.observer.send({
      fetch: context.fetch,
      variables: usedVariables,
      metadata: params.metadata,
      session: context.session,
      policy,
      stuff: {}
    });
    request.then((val) => {
      this.loadPending = false;
      params.then?.(val.data);
    }).catch(() => {
    });
    if (!fakeAwait) {
      await request;
    }
    return get(this.observer);
  }
}
async function fetchParams(artifact, storeName, params) {
  let policy = params?.policy;
  if (!policy && artifact.kind === ArtifactKind.Query) {
    policy = artifact.policy ?? CachePolicy.CacheOrNetwork;
  }
  let fetchFn = null;
  if (params) {
    if ("fetch" in params && params.fetch) {
      fetchFn = params.fetch;
    } else if ("event" in params && params.event && "fetch" in params.event) {
      fetchFn = params.event.fetch;
    }
  }
  if (!fetchFn) {
    fetchFn = globalThis.fetch.bind(globalThis);
  }
  let session = void 0;
  if (params && "event" in params && params.event) {
    session = await getSession(params.event);
  } else if (isBrowser) {
    session = await getSession();
  } else {
    log.error(contextError(storeName));
    throw new Error("Error, check above logs for help.");
  }
  return {
    context: {
      fetch: fetchFn,
      metadata: params?.metadata ?? {},
      session
    },
    policy,
    params: params ?? {}
  };
}
const contextError = (storeName) => `
	${log.red(`Missing event args in load function`)}.

Please remember to pass event to fetch like so:

import type { LoadEvent } from '@sveltejs/kit';

// in a load function...
export async function load(${log.yellow("event")}: LoadEvent) {
	return {
		...load_${storeName}({ ${log.yellow("event")}, variables: { ... } })
	};
}

// in a server-side mutation:
await mutation.mutate({ ... }, ${log.yellow("{ event }")})
`;
class QueryStoreCursor extends QueryStore {
  paginated = true;
  constructor(config) {
    super(config);
  }
  #_handlers = null;
  async #handlers() {
    if (this.#_handlers) {
      return this.#_handlers;
    }
    await initClient();
    const paginationObserver = getClient().observe({
      artifact: this.artifact
    });
    this.#_handlers = cursorHandlers({
      artifact: this.artifact,
      getState: () => get(this.observer).data,
      getVariables: () => get(this.observer).variables,
      fetch: super.fetch.bind(this),
      getSession,
      fetchUpdate: async (args, updates) => {
        await initClient();
        return paginationObserver.send({
          ...args,
          cacheParams: {
            applyUpdates: updates,
            disableSubscriptions: true,
            ...args?.cacheParams
          }
        });
      }
    });
    return this.#_handlers;
  }
  async fetch(args) {
    const handlers = await this.#handlers();
    return await handlers.fetch.call(this, args);
  }
  async loadPreviousPage(args) {
    const handlers = await this.#handlers();
    try {
      return await handlers.loadPreviousPage(args);
    } catch (e) {
      const err = e;
      if (err.name === "AbortError") {
        return get(this.observer);
      } else {
        throw err;
      }
    }
  }
  async loadNextPage(args) {
    const handlers = await this.#handlers();
    try {
      return await handlers.loadNextPage(args);
    } catch (e) {
      const err = e;
      if (err.name === "AbortError") {
        return get(this.observer);
      } else {
        throw err;
      }
    }
  }
  subscribe(run, invalidate) {
    const combined = derived([{ subscribe: super.subscribe.bind(this) }], ([$parent]) => {
      return {
        ...$parent,
        pageInfo: extractPageInfo($parent.data, this.artifact.refetch.path)
      };
    });
    return combined.subscribe(run, invalidate);
  }
}
class QueryStoreOffset extends QueryStore {
  paginated = true;
  async loadNextPage(args) {
    const handlers = await this.#handlers();
    return await handlers.loadNextPage.call(this, args);
  }
  async fetch(args) {
    const handlers = await this.#handlers();
    return await handlers.fetch.call(this, args);
  }
  #_handlers = null;
  async #handlers() {
    if (this.#_handlers) {
      return this.#_handlers;
    }
    await initClient();
    const paginationObserver = getClient().observe({
      artifact: this.artifact
    });
    this.#_handlers = offsetHandlers({
      artifact: this.artifact,
      storeName: this.name,
      fetch: super.fetch.bind(this),
      getState: () => get(this.observer).data,
      getVariables: () => get(this.observer).variables,
      getSession,
      fetchUpdate: async (args) => {
        await initClient();
        return paginationObserver.send({
          ...args,
          variables: {
            ...args?.variables
          },
          cacheParams: {
            applyUpdates: ["append"]
          }
        });
      }
    });
    return this.#_handlers;
  }
}
export {
  QueryStore,
  QueryStoreCursor,
  QueryStoreOffset,
  fetchParams
};
