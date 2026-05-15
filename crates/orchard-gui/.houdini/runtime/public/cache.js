import { getCurrentConfig, marshalInputs } from "../lib";
import { ListCollection } from "./list";
import { Record } from "./record";
class Cache {
  _internal_unstable;
  constructor(cache) {
    this._internal_unstable = cache;
  }
  // return the record proxy for the given type/id combo
  get(type, data) {
    let recordID = this._internal_unstable._internal_unstable.id(type, data);
    if (!recordID) {
      throw new Error("todo");
    }
    return new Record({
      cache: this,
      type,
      id: recordID,
      idFields: data
    });
  }
  get config() {
    return getCurrentConfig();
  }
  list(name, { parentID, allLists } = {}) {
    return new ListCollection({
      cache: this,
      name,
      parentID,
      allLists
    });
  }
  read({
    query,
    variables
  }) {
    return this._internal_unstable.read({
      selection: query.artifact.selection,
      variables
    });
  }
  write({
    query,
    variables,
    data
  }) {
    this._internal_unstable.write({
      selection: query.artifact.selection,
      // @ts-expect-error
      data,
      variables: marshalInputs({
        config: this.config,
        artifact: query.artifact,
        input: variables
      }) ?? {}
    });
    return;
  }
  /**
   * Mark some elements of the cache stale.
   */
  markStale(type, options) {
    return this._internal_unstable.markTypeStale(type ? { ...options, type } : void 0);
  }
  /**
   * Reset the entire cache by clearing all records and lists
   */
  reset() {
    return this._internal_unstable.reset();
  }
}
export {
  Cache
};
