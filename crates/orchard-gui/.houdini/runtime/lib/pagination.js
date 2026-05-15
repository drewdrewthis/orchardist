import { deepEquals } from "./deepEquals";
import { countPage, extractPageInfo, missingPageSizeError } from "./pageInfo";
import { CachePolicy, DataSource } from "./types";
function cursorHandlers({
  artifact,
  fetchUpdate: parentFetchUpdate,
  fetch: parentFetch,
  getState,
  getVariables,
  getSession
}) {
  const loadPage = async ({
    pageSizeVar,
    input,
    functionName,
    metadata = {},
    fetch,
    where
  }) => {
    const loadVariables = {
      ...getVariables(),
      ...input
    };
    if (!loadVariables[pageSizeVar] && !artifact.refetch.pageSize) {
      throw missingPageSizeError(functionName);
    }
    let isSinglePage = artifact.refetch?.mode === "SinglePage";
    return (isSinglePage ? parentFetch : parentFetchUpdate)(
      {
        variables: loadVariables,
        fetch,
        metadata,
        policy: isSinglePage ? artifact.policy : CachePolicy.NetworkOnly,
        session: await getSession()
      },
      isSinglePage ? [] : [where === "start" ? "prepend" : "append"]
    );
  };
  const getPageInfo = () => {
    return extractPageInfo(getState(), artifact.refetch?.path ?? []);
  };
  return {
    loadNextPage: ({
      first,
      after,
      fetch,
      metadata
    } = {}) => {
      const currentPageInfo = getPageInfo();
      if (!currentPageInfo.hasNextPage) {
        return Promise.resolve({
          data: getState(),
          errors: null,
          fetching: false,
          partial: false,
          stale: false,
          source: DataSource.Cache,
          variables: getVariables()
        });
      }
      const input = {
        first: first ?? artifact.refetch.pageSize,
        after: after ?? currentPageInfo.endCursor,
        before: null,
        last: null
      };
      return loadPage({
        pageSizeVar: "first",
        functionName: "loadNextPage",
        input,
        fetch,
        metadata,
        where: "end"
      });
    },
    loadPreviousPage: ({
      last,
      before,
      fetch,
      metadata
    } = {}) => {
      const currentPageInfo = getPageInfo();
      if (!currentPageInfo.hasPreviousPage) {
        return Promise.resolve({
          data: getState(),
          errors: null,
          fetching: false,
          partial: false,
          stale: false,
          source: DataSource.Cache,
          variables: getVariables()
        });
      }
      const input = {
        before: before ?? currentPageInfo.startCursor,
        last: last ?? artifact.refetch.pageSize,
        first: null,
        after: null
      };
      return loadPage({
        pageSizeVar: "last",
        functionName: "loadPreviousPage",
        input,
        fetch,
        metadata,
        where: "start"
      });
    },
    async fetch(args) {
      const { variables } = args ?? {};
      if (variables && !deepEquals(getVariables(), variables)) {
        return await parentFetch(args);
      }
      try {
        var currentPageInfo = extractPageInfo(getState(), artifact.refetch.path);
      } catch {
        return await parentFetch(args);
      }
      const queryVariables = {};
      const count = countPage(artifact.refetch.path.concat("edges"), getState()) || artifact.refetch.pageSize;
      if (count && count > artifact.refetch.pageSize) {
        if (currentPageInfo.hasPreviousPage && currentPageInfo.hasNextPage && // only log if they haven't provided special parameters
        !(variables?.["first"] && variables?.["after"] || variables?.["last"] && variables?.["before"])) {
          console.warn(`\u26A0\uFE0F Encountered a fetch() in the middle of the connection.
Make sure to pass a cursor value by hand that includes the current set (ie the entry before startCursor)
`);
        }
        if (!currentPageInfo.hasPreviousPage) {
          queryVariables["first"] = count;
          queryVariables["after"] = null;
          queryVariables["last"] = null;
          queryVariables["before"] = null;
        } else if (!currentPageInfo.hasNextPage) {
          queryVariables["last"] = count;
          queryVariables["first"] = null;
          queryVariables["after"] = null;
          queryVariables["before"] = null;
        }
      }
      Object.assign(queryVariables, variables ?? {});
      const result = await parentFetch({
        ...args,
        variables: queryVariables
      });
      return result;
    }
  };
}
function offsetHandlers({
  artifact,
  storeName,
  getState,
  getVariables,
  fetch: parentFetch,
  fetchUpdate: parentFetchUpdate,
  getSession
}) {
  let isSinglePage = artifact.refetch?.mode === "SinglePage";
  let getOffset = () => {
    let offset = artifact.refetch?.start || countPage(artifact.refetch.path, getState()) || artifact.refetch.pageSize;
    if (isSinglePage) {
      offset += getVariables()?.offset ?? 0;
    }
    return offset;
  };
  let currentOffset = getOffset() ?? 0;
  return {
    loadNextPage: async ({
      limit,
      offset,
      fetch,
      metadata
    } = {}) => {
      const queryVariables = {
        ...getVariables(),
        offset: offset ?? getOffset()
      };
      if (limit || limit === 0) {
        queryVariables.limit = limit;
      }
      if (!queryVariables.limit && !artifact.refetch.pageSize) {
        throw missingPageSizeError("loadNextPage");
      }
      const targetFetch = isSinglePage ? parentFetch : parentFetchUpdate;
      await targetFetch({
        variables: queryVariables,
        fetch,
        metadata,
        policy: isSinglePage ? artifact.policy : CachePolicy.NetworkOnly,
        session: await getSession()
      });
      const pageSize = queryVariables.limit || artifact.refetch.pageSize;
      currentOffset = offset + pageSize;
    },
    async fetch(params = {}) {
      const { variables } = params;
      if (variables && !deepEquals(getVariables(), variables)) {
        return parentFetch.call(this, params);
      }
      const count = currentOffset || getOffset();
      const queryVariables = {};
      if (!artifact.refetch.pageSize || count > artifact.refetch.pageSize) {
        queryVariables.limit = count;
      }
      return await parentFetch.call(this, {
        ...params,
        variables: queryVariables
      });
    }
  };
}
export {
  cursorHandlers,
  offsetHandlers
};
