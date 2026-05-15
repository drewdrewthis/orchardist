const CachePolicy = {
  CacheOrNetwork: "CacheOrNetwork",
  CacheOnly: "CacheOnly",
  NetworkOnly: "NetworkOnly",
  CacheAndNetwork: "CacheAndNetwork",
  NoCache: "NoCache"
};
const DedupeMatchMode = {
  Variables: "Variables",
  Operation: "Operation",
  None: "None"
};
const PaginateMode = {
  Infinite: "Infinite",
  SinglePage: "SinglePage"
};
export * from "../router/types";
const ArtifactKind = {
  Query: "HoudiniQuery",
  Subscription: "HoudiniSubscription",
  Mutation: "HoudiniMutation",
  Fragment: "HoudiniFragment"
};
const CompiledFragmentKind = ArtifactKind.Fragment;
const CompiledMutationKind = ArtifactKind.Mutation;
const CompiledQueryKind = ArtifactKind.Query;
const CompiledSubscriptionKind = ArtifactKind.Subscription;
const RefetchUpdateMode = {
  append: "append",
  prepend: "prepend",
  replace: "replace"
};
const DataSource = {
  /**
   * from the browser cache
   */
  Cache: "cache",
  /**
   * from a browser side `fetch`
   */
  Network: "network",
  /**
   * from a server side `fetch`
   */
  Ssr: "ssr"
};
const fragmentKey = " $fragments";
const PendingValue = Symbol("houdini_loading");
function isPending(value) {
  return typeof value === "symbol";
}
export {
  ArtifactKind,
  CachePolicy,
  CompiledFragmentKind,
  CompiledMutationKind,
  CompiledQueryKind,
  CompiledSubscriptionKind,
  DataSource,
  DedupeMatchMode,
  PaginateMode,
  PendingValue,
  RefetchUpdateMode,
  fragmentKey,
  isPending
};
