import cache from "$houdini/runtime/cache";
import { getCurrentConfig, keyFieldsForType } from "$houdini/runtime/lib/config";
import { siteURL } from "$houdini/runtime/lib/constants";
import { extractPageInfo } from "$houdini/runtime/lib/pageInfo";
import { cursorHandlers, offsetHandlers } from "$houdini/runtime/lib/pagination";
import { marshalInputs } from "$houdini/runtime/lib/scalars";
import { CompiledFragmentKind, fragmentKey } from "$houdini/runtime/lib/types";
import { get, derived } from "svelte/store";
import { isBrowser } from "../adapter";
import { getClient, initClient } from "../client";
import { getSession } from "../session";
import { BaseStore } from "./base";
class FragmentStore {
  artifact;
  name;
  kind = CompiledFragmentKind;
  context = null;
  constructor({ artifact, storeName }) {
    this.artifact = artifact;
    this.name = storeName;
  }
  get(initialValue) {
    const { variables, parent } = initialValue?.[fragmentKey]?.values?.[this.artifact.name] ?? {};
    const { loading } = initialValue?.[fragmentKey] ?? {};
    if (!loading && initialValue && fragmentKey in initialValue && (!variables || !parent) && isBrowser) {
      console.warn(
        `\u26A0\uFE0F Parent does not contain the information for this fragment. Something is wrong.
Please ensure that you have passed a record that has ${this.artifact.name} mixed into it.`
      );
    }
    let data = initialValue;
    if (loading || initialValue && parent && isBrowser) {
      data = cache.read({
        selection: this.artifact.selection,
        parent,
        variables,
        loading
      }).data;
    }
    const store = new BaseStore({
      artifact: this.artifact,
      initialValue: data
    });
    if (!loading && parent) {
      store.observer.send({ variables, setup: true, stuff: { parentID: parent } });
    }
    return {
      initialValue: data,
      variables: marshalInputs({
        artifact: this.artifact,
        input: variables,
        config: getCurrentConfig(),
        rootType: this.artifact.rootType
      }),
      kind: CompiledFragmentKind,
      subscribe: derived([store], ([$store]) => $store.data).subscribe
    };
  }
}
class BasePaginatedFragmentStore {
  paginated = true;
  paginationArtifact;
  name;
  kind = CompiledFragmentKind;
  artifact;
  constructor(config) {
    this.paginationArtifact = config.paginationArtifact;
    this.name = config.storeName;
    this.artifact = config.artifact;
  }
  queryVariables(getState) {
    const config = getCurrentConfig();
    const { targetType } = this.paginationArtifact.refetch || {};
    const typeConfig = config.types?.[targetType || ""];
    if (!typeConfig) {
      throw new Error(
        `Missing type refetch configuration for ${targetType}. For more information, see ${siteURL}/guides/pagination#paginated-fragments`
      );
    }
    let idVariables = {};
    const value = getState();
    if (typeConfig.resolve?.arguments) {
      idVariables = typeConfig.resolve.arguments?.(value) || {};
    } else {
      const keys = keyFieldsForType(config, targetType || "");
      idVariables = Object.fromEntries(keys.map((key) => [key, value[key]]));
    }
    return {
      ...idVariables
    };
  }
}
class FragmentStoreCursor extends BasePaginatedFragmentStore {
  get(initialValue) {
    const base = new FragmentStore({
      artifact: this.artifact,
      storeName: this.name
    });
    const store = base.get(initialValue);
    const paginationStore = getClient().observe({
      artifact: this.paginationArtifact,
      initialValue: store.initialValue
    });
    const handlers = this.storeHandlers(
      paginationStore,
      initialValue,
      () => get(store),
      () => store.variables
    );
    const subscribe = (run, invalidate) => {
      const combined = derived([store, paginationStore], ([$parent, $pagination]) => {
        return {
          ...$pagination,
          data: $parent,
          pageInfo: extractPageInfo($parent, this.paginationArtifact.refetch.path)
        };
      });
      return combined.subscribe(run, invalidate);
    };
    return {
      kind: CompiledFragmentKind,
      subscribe,
      fetch: handlers.fetch,
      loadNextPage: handlers.loadNextPage,
      loadPreviousPage: handlers.loadPreviousPage
    };
  }
  storeHandlers(observer, initialValue, getState, getVariables) {
    return cursorHandlers({
      getState,
      getVariables,
      artifact: this.paginationArtifact,
      fetchUpdate: async (args, updates) => {
        await initClient();
        return observer.send({
          session: await getSession(),
          ...args,
          variables: {
            ...args?.variables,
            ...this.queryVariables(getState)
          },
          cacheParams: {
            applyUpdates: updates,
            disableSubscriptions: true
          }
        });
      },
      fetch: async (args) => {
        await initClient();
        return await observer.send({
          session: await getSession(),
          ...args,
          variables: {
            ...args?.variables,
            ...this.queryVariables(getState)
          },
          cacheParams: {
            disableSubscriptions: true
          }
        });
      },
      getSession
    });
  }
}
class FragmentStoreOffset extends BasePaginatedFragmentStore {
  get(initialValue) {
    const base = new FragmentStore({
      artifact: this.artifact,
      storeName: this.name
    });
    const store = base.get(initialValue);
    const paginationStore = getClient().observe({
      artifact: this.paginationArtifact,
      initialValue: store.initialValue
    });
    const getState = () => get(store);
    const handlers = offsetHandlers({
      getState,
      getVariables: () => store.variables,
      artifact: this.paginationArtifact,
      fetch: async (args) => {
        await initClient();
        return paginationStore.send({
          ...args,
          session: await getSession(),
          variables: {
            ...this.queryVariables(getState),
            ...args?.variables
          },
          cacheParams: {
            disableSubscriptions: true
          }
        });
      },
      fetchUpdate: async (args) => {
        await initClient();
        return paginationStore.send({
          session: await getSession(),
          ...args,
          variables: {
            ...this.queryVariables(getState),
            ...args?.variables
          },
          cacheParams: {
            disableSubscriptions: true,
            applyUpdates: ["append"]
          }
        });
      },
      getSession,
      storeName: this.name
    });
    const subscribe = (run, invalidate) => {
      const combined = derived([store, paginationStore], ([$parent, $pagination]) => {
        return {
          ...$pagination,
          data: $parent
        };
      });
      return combined.subscribe(run, invalidate);
    };
    return {
      kind: CompiledFragmentKind,
      data: derived(paginationStore, ($value) => $value.data),
      subscribe,
      fetch: handlers.fetch,
      loadNextPage: handlers.loadNextPage,
      fetching: derived(paginationStore, ($store) => $store.fetching)
    };
  }
}
export {
  FragmentStore,
  FragmentStoreCursor,
  FragmentStoreOffset
};
