import { computeKey, PendingValue } from "../lib";
import { computeID, defaultConfigValues, keyFieldsForType, getCurrentConfig } from "../lib/config";
import { deepEquals } from "../lib/deepEquals";
import { flatten } from "../lib/flatten";
import { getFieldsForType } from "../lib/selection";
import { fragmentKey } from "../lib/types";
import { GarbageCollector } from "./gc";
import { ListManager } from "./lists";
import { StaleManager } from "./staleManager";
import { InMemoryStorage } from "./storage";
import { evaluateKey, rootID } from "./stuff";
import { InMemorySubscriptions } from "./subscription";
class Cache {
  // the internal implementation for a lot of the cache's methods are moved into
  // a second class to avoid users from relying on unstable APIs. typescript's private
  // label accomplishes this but would not prevent someone using vanilla js
  _internal_unstable;
  constructor({
    disabled,
    componentCache,
    createComponent,
    ...config
  } = {}) {
    this._internal_unstable = new CacheInternal({
      cache: this,
      storage: new InMemoryStorage(),
      subscriptions: new InMemorySubscriptions(this),
      lists: new ListManager(this, rootID),
      lifetimes: new GarbageCollector(this),
      staleManager: new StaleManager(this),
      disabled: disabled ?? typeof globalThis.window === "undefined",
      componentCache,
      createComponent
    });
    if (Object.keys(config).length > 0) {
      this.setConfig(defaultConfigValues(config));
    }
  }
  // walk down the selection and save the values that we encounter.
  // any changes will notify subscribers. writing to an optimistic layer will resolve it
  write({
    layer: layerID,
    notifySubscribers = [],
    ...args
  }) {
    const layer = layerID ? this._internal_unstable.storage.getLayer(layerID) : this._internal_unstable.storage.topLayer;
    const subscribers = this._internal_unstable.writeSelection({ ...args, layer }).map((sub) => sub[0]);
    this.#notifySubscribers(subscribers.concat(notifySubscribers));
    return subscribers;
  }
  // reconstruct an object with the fields/relations specified by a selection
  read(...args) {
    const { data, partial, stale, hasData } = this._internal_unstable.getSelection(...args);
    if (!hasData) {
      return { data: null, partial: false, stale: false };
    }
    return {
      data,
      partial,
      stale
    };
  }
  // register the provided callbacks with the fields specified by the selection
  subscribe(spec, variables = {}) {
    if (this._internal_unstable.disabled) {
      return;
    }
    return this._internal_unstable.subscriptions.add({
      parent: spec.parentID || rootID,
      spec,
      selection: spec.selection,
      variables
    });
  }
  // stop listening to a particular subscription
  unsubscribe(spec, variables = {}) {
    return this._internal_unstable.subscriptions.remove(
      spec.parentID || rootID,
      spec.selection,
      [spec],
      variables
    );
  }
  // return the list handler to mutate a named list in the cache
  list(name, parentID, allLists, skipMatches) {
    const handler = this._internal_unstable.lists.get(name, parentID, allLists, skipMatches);
    if (!handler) {
      throw new Error(
        `Cannot find list with name: ${name}${parentID ? " under parent " + parentID : ""}. Is it possible that the query is not mounted?`
      );
    }
    return handler;
  }
  // when an optimistic key resolves, we might momentarily know the same record by different IDs
  registerKeyMap(source, mapped) {
    this._internal_unstable.storage.registerIDMapping(source, mapped);
    this._internal_unstable.subscriptions.copySubscribers(source, mapped);
  }
  // remove the record from the cache's store and unsubscribe from it
  delete(id, layer) {
    const recordIDs = [this._internal_unstable.storage.idMaps[id], id].filter(
      Boolean
    );
    for (const recordID of recordIDs) {
      this._internal_unstable.subscriptions.removeAllSubscribers(recordID);
      this._internal_unstable.lists.removeIDFromAllLists(recordID, layer);
      this._internal_unstable.storage.delete(recordID, layer);
    }
  }
  // set the cache's config
  setConfig(config) {
    this._internal_unstable.setConfig(config);
  }
  markTypeStale(options) {
    if (!options) {
      this._internal_unstable.staleManager.markAllStale();
    } else if (!options.field) {
      this._internal_unstable.staleManager.markTypeStale(options.type);
    } else {
      this._internal_unstable.staleManager.markTypeFieldStale(
        options.type,
        options.field,
        options.when
      );
    }
  }
  markRecordStale(id, options) {
    if (options.field) {
      const key = computeKey({ field: options.field, args: options.when ?? {} });
      this._internal_unstable.staleManager.markFieldStale(id, key);
    } else {
      this._internal_unstable.staleManager.markRecordStale(id);
    }
  }
  getFieldTime(id, field) {
    return this._internal_unstable.staleManager.getFieldTime(id, field);
  }
  config() {
    return this._internal_unstable.config;
  }
  serialize() {
    return this._internal_unstable.storage.serialize();
  }
  hydrate(...args) {
    return this._internal_unstable.storage.hydrate(...args);
  }
  clearLayer(layerID) {
    const layer = this._internal_unstable.storage.getLayer(layerID);
    if (!layer) {
      throw new Error("Cannot find layer with id: " + layerID);
    }
    const toNotify = [];
    const allFields = [];
    for (const target of [layer.fields, layer.links]) {
      for (const [id, fields] of Object.entries(target)) {
        allFields.push(
          ...Object.entries(fields).map(([field, value]) => ({ id, field, value }))
        );
      }
    }
    const displayFields = [];
    for (const pair of allFields) {
      const { displayLayers } = this._internal_unstable.storage.get(pair.id, pair.field);
      if (!displayLayers.includes(layerID)) {
        continue;
      }
      displayFields.push(pair);
    }
    for (const [id, operation] of Object.entries(layer.operations)) {
      if (operation.deleted) {
        displayFields.push(
          ...this._internal_unstable.subscriptions.activeFields(id).map((field) => ({ id, field }))
        );
      }
      const fields = Object.keys(operation.fields ?? {});
      if (fields.length > 0) {
        displayFields.push(...fields.map((field) => ({ id, field })));
      }
    }
    layer.clear();
    for (const display of displayFields) {
      const { field, id } = display;
      const notify = !("value" in display) || this._internal_unstable.storage.get(id, field).value !== display.value;
      if (notify) {
        toNotify.push(
          ...this._internal_unstable.subscriptions.get(id, field).map((sub) => sub[0])
        );
      }
    }
    this.#notifySubscribers(toNotify);
  }
  // reset the whole cache
  reset() {
    const subSpecs = this._internal_unstable.subscriptions.reset();
    this._internal_unstable.staleManager.reset();
    this._internal_unstable.lifetimes.reset();
    this._internal_unstable.lists.reset();
    this._internal_unstable.storage.reset();
    this.#notifySubscribers(subSpecs);
  }
  #notifySubscribers(subs) {
    if (subs.length === 0) {
      return;
    }
    const notified = [];
    for (const spec of subs) {
      if (!notified.includes(spec.set)) {
        notified.push(spec.set);
        spec.set(
          this._internal_unstable.getSelection({
            parent: spec.parentID || rootID,
            selection: spec.selection,
            variables: spec.variables?.() || {},
            ignoreMasking: false
          }).data
        );
      }
    }
  }
}
class CacheInternal {
  // for server-side requests we need to be able to flag the cache as disabled so we dont write to it
  disabled = false;
  _config;
  storage;
  subscriptions;
  lists;
  cache;
  lifetimes;
  staleManager;
  componentCache;
  createComponent;
  constructor({
    storage,
    subscriptions,
    lists,
    cache,
    lifetimes,
    staleManager,
    disabled,
    config,
    componentCache,
    createComponent
  }) {
    this.storage = storage;
    this.subscriptions = subscriptions;
    this.lists = lists;
    this.cache = cache;
    this.lifetimes = lifetimes;
    this.staleManager = staleManager;
    this._config = config;
    this.componentCache = componentCache ?? {};
    this.createComponent = createComponent ?? (() => ({}));
    this.disabled = disabled;
    try {
      if (process.env.HOUDINI_TEST === "true") {
        this.disabled = false;
      }
    } catch {
    }
  }
  get config() {
    return this._config ?? getCurrentConfig();
  }
  setConfig(config) {
    this._config = config;
  }
  writeSelection({
    data,
    selection,
    variables = {},
    parent = rootID,
    applyUpdates,
    layer,
    toNotify = [],
    forceNotify,
    forceStale
  }) {
    if (this.disabled) {
      return [];
    }
    let targetSelection = getFieldsForType(
      selection,
      data["__typename"],
      false
    );
    for (const [field, value] of Object.entries(data)) {
      if (!selection || !targetSelection[field]) {
        continue;
      }
      let {
        type: linkedType,
        keyRaw,
        selection: fieldSelection,
        operations,
        abstract: isAbstract,
        updates
      } = targetSelection[field];
      const key = evaluateKey(keyRaw, variables);
      if (value && typeof value === "object" && "__typename" in value && value["__typename"]) {
        linkedType = value["__typename"];
      }
      const currentSubscribers = this.subscriptions.get(parent, key);
      const specs = currentSubscribers.map((sub) => sub[0]);
      const { value: previousValue, displayLayers } = this.storage.get(parent, key);
      const displayLayer = layer.isDisplayLayer(displayLayers);
      if (displayLayer) {
        this.lifetimes.resetLifetime(parent, key);
        if (forceStale) {
          this.staleManager.markFieldStale(parent, key);
        } else {
          this.staleManager.setFieldTimeToNow(parent, key);
        }
      }
      if (!fieldSelection) {
        let newValue = value;
        if (updates && applyUpdates && Array.isArray(value)) {
          for (const update of applyUpdates) {
            if (!updates.includes(update)) {
              continue;
            }
            if (update === "append") {
              newValue = (previousValue || []).concat(value);
            } else if (update === "prepend") {
              newValue = value.concat(previousValue || []);
            }
          }
        }
        if (updates && applyUpdates?.includes("prepend") && ["endCursor", "hasNextPage"].includes(key)) {
          newValue = previousValue;
        } else if (updates && applyUpdates?.includes("append") && ["startCursor", "hasPreviousPage"].includes(key)) {
          newValue = previousValue;
        }
        const valueChanged = !deepEquals(newValue, previousValue);
        if (displayLayer && (valueChanged || forceNotify)) {
          toNotify.push(...currentSubscribers);
        }
        layer.writeField(parent, key, newValue);
      } else if (value === null) {
        if (previousValue === null) {
          continue;
        }
        const previousLinks = flatten([previousValue]);
        for (const link of previousLinks) {
          this.subscriptions.remove(link, fieldSelection, specs, variables);
        }
        layer.writeLink(parent, key, null);
        toNotify.push(...currentSubscribers);
      } else if (value instanceof Object && !Array.isArray(value)) {
        if (isAbstract) {
          if (!value.__typename) {
            throw new Error(
              "Encountered interface type without __typename in the payload"
            );
          }
        }
        let linkedID = null;
        if (value !== null) {
          linkedID = !this.isEmbedded(linkedType, value) ? this.id(linkedType, value) : `${parent}.${key}`;
        }
        let linkChange = linkedID !== previousValue;
        layer.writeLink(parent, key, linkedID);
        if (linkedID && displayLayer && (linkChange || forceNotify)) {
          if (previousValue && typeof previousValue === "string") {
            this.subscriptions.remove(previousValue, fieldSelection, specs, variables);
          }
          this.subscriptions.addMany({
            parent: linkedID,
            subscribers: currentSubscribers,
            variables,
            parentType: linkedType
          });
          toNotify.push(...currentSubscribers);
        }
        if (linkedID) {
          this.writeSelection({
            selection: fieldSelection,
            parent: linkedID,
            data: value,
            variables,
            toNotify,
            applyUpdates,
            layer,
            forceNotify
          });
        }
      } else if (Array.isArray(value) && // make typescript happy
      (typeof previousValue === "undefined" || previousValue === null || Array.isArray(previousValue))) {
        let oldIDs = [...previousValue || []];
        if (updates?.includes("append") || updates?.includes("prepend")) {
          oldIDs = oldIDs.filter((id) => {
            for (const layer2 of this.storage.data) {
              for (const operation of Object.values(layer2.operations)) {
                if (operation.fields?.[key])
                  for (const listOperation of operation.fields[key]) {
                    if ("id" in listOperation && listOperation.id === id) {
                      return false;
                    }
                  }
              }
            }
            return true;
          });
        }
        let linkedIDs = [];
        const { newIDs, nestedIDs } = this.extractNestedListIDs({
          value,
          abstract: Boolean(isAbstract),
          specs: toNotify,
          applyUpdates,
          recordID: parent,
          key,
          linkedType,
          variables,
          fields: fieldSelection,
          layer,
          forceNotify
        });
        let action = () => {
          layer.writeLink(parent, key, linkedIDs);
        };
        if (applyUpdates && updates) {
          const filterIDs = (keep, insert) => {
            const existingIDs = /* @__PURE__ */ new Set();
            for (const id of keep) {
              if (!id) {
                continue;
              }
              const { value: node } = this.storage.get(id, "node");
              if (!node) {
                continue;
              }
              const nodeID = this.storage.get(node, "id");
              if (!nodeID) {
                continue;
              }
              existingIDs.add(nodeID.value);
            }
            return insert.filter((id) => {
              if (!id) {
                return true;
              }
              const { value: node } = this.storage.get(id, "node");
              if (!node) {
                return true;
              }
              const nodeID = this.storage.get(node, "id");
              if (!nodeID) {
                return true;
              }
              return !existingIDs.has(nodeID.value);
            });
          };
          for (const update of applyUpdates) {
            if (update !== "replace" && !updates.includes(update)) {
              continue;
            }
            if (update === "prepend") {
              linkedIDs = newIDs.concat(
                filterIDs(newIDs, oldIDs)
              );
              if (layer?.optimistic) {
                action = () => {
                  for (const id of newIDs) {
                    if (id) {
                      layer.insert(parent, key, "start", id);
                    }
                  }
                };
              }
            } else if (update === "append") {
              linkedIDs = filterIDs(newIDs, oldIDs).concat(newIDs);
              if (layer?.optimistic) {
                action = () => {
                  for (const id of newIDs) {
                    if (id) {
                      layer.insert(parent, key, "end", id);
                    }
                  }
                };
              }
            } else if (update === "replace") {
              linkedIDs = newIDs;
            }
          }
        } else {
          linkedIDs = nestedIDs;
        }
        const contentChanged = !deepEquals(linkedIDs, oldIDs) || previousValue === null;
        if (contentChanged || forceNotify) {
          toNotify.push(...currentSubscribers);
        }
        for (const lostID of oldIDs) {
          if (linkedIDs.includes(lostID) || !lostID) {
            continue;
          }
          this.subscriptions.remove(lostID, fieldSelection, specs, variables);
        }
        if (contentChanged || oldIDs.length === 0 && newIDs.length === 0) {
          action();
        }
        for (const id of newIDs.filter((id2) => !oldIDs.includes(id2))) {
          if (id == null) {
            continue;
          }
          this.subscriptions.addMany({
            parent: id,
            subscribers: currentSubscribers,
            variables,
            parentType: linkedType
          });
        }
      }
      const processedOperations = /* @__PURE__ */ new Set();
      for (const operation of operations || []) {
        let parentID;
        if (operation.parentID) {
          if (operation.parentID.kind !== "Variable") {
            parentID = operation.parentID.value;
          } else {
            const id = variables[operation.parentID.value];
            if (typeof id !== "string") {
              throw new Error("parentID value must be a string");
            }
            parentID = id;
          }
        }
        if (operation.list && !this.lists.get(operation.list, parentID, operation.target === "all")) {
          continue;
        }
        const targets = Array.isArray(value) ? value : [value];
        for (const target of targets) {
          if (operation.action === "insert" && target instanceof Object && fieldSelection && operation.list) {
            this.cache.list(
              operation.list,
              parentID,
              operation.target === "all",
              processedOperations
            ).when(operation.when).addToList(
              fieldSelection,
              target,
              variables,
              operation.position || "last",
              layer
            );
          } else if (operation.action === "toggle" && target instanceof Object && fieldSelection && operation.list) {
            this.cache.list(
              operation.list,
              parentID,
              operation.target === "all",
              processedOperations
            ).when(operation.when).toggleElement({
              selection: fieldSelection,
              data: target,
              variables,
              where: operation.position || "last",
              layer
            });
          } else if (operation.action === "remove" && target instanceof Object && fieldSelection && operation.list) {
            this.cache.list(
              operation.list,
              parentID,
              operation.target === "all",
              processedOperations
            ).when(operation.when).remove(target, variables, layer);
          } else if (operation.action === "delete" && operation.type && target) {
            const targetID = this.id(operation.type, target);
            if (!targetID) {
              continue;
            }
            toNotify.push(
              ...this.subscriptions.getAll(targetID).filter((sub) => sub[0].parentID !== targetID)
            );
            this.cache.delete(targetID, layer);
          }
        }
        if (operation.list) {
          const matchingLists = this.cache.list(
            operation.list,
            parentID,
            operation.target === "all"
          );
          for (const list of matchingLists.lists) {
            processedOperations.add(list.fieldRef);
          }
        }
      }
    }
    return toNotify;
  }
  // reconstruct an object defined by its selection
  getSelection({
    selection,
    parent = rootID,
    variables,
    stepsFromConnection = null,
    ignoreMasking,
    fullCheck = false,
    loading: generateLoading
  }) {
    if (parent === null) {
      return { data: null, partial: false, stale: false, hasData: true };
    }
    const target = {};
    if (selection.fragments) {
      target[fragmentKey] = {
        loading: Boolean(generateLoading),
        values: Object.fromEntries(
          Object.entries(selection.fragments).filter(([, value]) => !generateLoading || value.loading).map(([key, value]) => [
            key,
            {
              parent,
              variables: evaluateVariables(value.arguments, variables ?? {})
            }
          ])
        )
      };
    }
    let hasData = !!selection.fragments;
    let partial = false;
    let cascadeNull = false;
    let stale = false;
    const typename = this.storage.get(parent, "__typename").value;
    let targetSelection = getFieldsForType(selection, typename, !!generateLoading);
    for (const [
      attributeName,
      {
        type,
        keyRaw,
        selection: fieldSelection,
        nullable,
        list,
        visible,
        directives,
        loading: fieldLoading,
        abstractHasRequired,
        component
      }
    ] of Object.entries(targetSelection)) {
      if (!visible && !ignoreMasking && !fullCheck) {
        continue;
      }
      const includeDirective = directives?.find((d) => {
        return d.name === "include";
      });
      if (includeDirective) {
        if (!evaluateVariables(includeDirective.arguments, variables ?? {})["if"]) {
          continue;
        }
      }
      const skipDirective = directives?.find((d) => {
        return d.name === "skip";
      });
      if (skipDirective) {
        if (evaluateVariables(skipDirective.arguments, variables ?? {})["if"]) {
          continue;
        }
      }
      const fieldTarget = visible || ignoreMasking ? target : {};
      const key = evaluateKey(keyRaw, variables);
      if (generateLoading && !fieldLoading) {
        continue;
      }
      const defaultValue = !component ? void 0 : defaultComponentField({
        cache: this.cache,
        component,
        variables,
        parent
      });
      let { value } = this.storage.get(parent, key, defaultValue);
      const dt_field = this.staleManager.getFieldTime(parent, key);
      if (dt_field === null) {
        stale = true;
      }
      if (generateLoading) {
        value = void 0;
      }
      let nextStep = stepsFromConnection;
      if (nextStep !== null) {
        if (nextStep >= 2) {
          nextStep = null;
        } else {
          nextStep += 1;
        }
      }
      if (list?.connection) {
        nextStep = 0;
      }
      const embeddedCursor = key === "cursor" && stepsFromConnection === 1;
      if (typeof value === "undefined" && !embeddedCursor) {
        partial = true;
      }
      if (generateLoading && fieldLoading?.kind === "value") {
        fieldTarget[attributeName] = PendingValue;
        hasData = true;
      } else if (!generateLoading && typeof value === "undefined" || value === null) {
        fieldTarget[attributeName] = null;
        if (typeof value !== "undefined") {
          hasData = true;
        }
      } else if (!fieldSelection) {
        const fnUnmarshal = this.config?.scalars?.[type]?.unmarshal;
        if (fnUnmarshal) {
          if (Array.isArray(value)) {
            fieldTarget[attributeName] = value.map(
              (v) => fnUnmarshal(v)
            );
          } else {
            fieldTarget[attributeName] = fnUnmarshal(value);
          }
        } else {
          fieldTarget[attributeName] = value;
        }
        hasData = true;
      } else if (Array.isArray(value)) {
        const listValue = this.hydrateNestedList({
          fields: fieldSelection,
          variables,
          linkedList: value,
          stepsFromConnection: nextStep,
          ignoreMasking: !!ignoreMasking,
          fullCheck,
          loading: generateLoading,
          nullable: !!nullable
        });
        fieldTarget[attributeName] = listValue.data;
        if (listValue.partial) {
          partial = true;
        }
        if (listValue.cascadeNull) {
          cascadeNull = true;
        }
        if (listValue.stale) {
          stale = true;
        }
        if (listValue.hasData || value.length === 0) {
          hasData = true;
        }
      } else {
        const objectFields = this.getSelection({
          parent: value,
          selection: fieldSelection,
          variables,
          stepsFromConnection: nextStep,
          ignoreMasking,
          fullCheck,
          loading: generateLoading
        });
        fieldTarget[attributeName] = objectFields.data;
        if (objectFields.partial) {
          partial = true;
        }
        if (objectFields.stale) {
          stale = true;
        }
        if (objectFields.hasData) {
          hasData = true;
        }
      }
      if (generateLoading && fieldLoading?.list) {
        fieldTarget[attributeName] = wrapInLists(
          Array.from({ length: fieldLoading.list.count }).fill(
            fieldTarget[attributeName]
          ),
          fieldLoading.list.depth - 1
        );
      }
      if (fieldTarget[attributeName] === null && !nullable && !embeddedCursor) {
        if (abstractHasRequired) {
          target[attributeName] = {
            __typename: "@required field missing; don't match this"
          };
        } else {
          cascadeNull = true;
        }
      }
    }
    return {
      data: cascadeNull ? null : target,
      // our value is considered true if there is some data but not everything
      // has a full value
      partial: !generateLoading && hasData && partial,
      stale: hasData && stale,
      hasData
    };
  }
  id(type, data) {
    const id = typeof data === "object" ? this.computeID(type, data) : data;
    if (!id) {
      return null;
    }
    if (!type) {
      return id;
    }
    return type + ":" + id;
  }
  // the list of fields that we need in order to compute an objects id
  idFields(type) {
    return keyFieldsForType(this.config, type);
  }
  computeID(type, data) {
    return computeID(this.config, type, data);
  }
  // figure out if this is an embedded object or a linked one by looking for all of the fields marked as
  // required to compute the entity's id
  isEmbedded(linkedType, value) {
    const idFields = this.idFields(linkedType);
    return idFields.length === 0 || idFields.filter((field) => typeof value[field] === "undefined").length > 0;
  }
  hydrateNestedList({
    fields,
    variables,
    linkedList,
    stepsFromConnection,
    ignoreMasking,
    fullCheck,
    loading,
    nullable
  }) {
    const result = [];
    let partialData = false;
    let stale = false;
    let hasValues = false;
    let cascadeNull = false;
    for (const entry of linkedList) {
      if (Array.isArray(entry)) {
        const nestedValue = this.hydrateNestedList({
          fields,
          nullable,
          variables,
          linkedList: entry,
          stepsFromConnection,
          ignoreMasking,
          fullCheck,
          loading
        });
        result.push(nestedValue.data);
        if (nestedValue.partial) {
          partialData = true;
        }
        if (nestedValue.cascadeNull) {
          cascadeNull = true;
        }
        continue;
      }
      if (entry === null) {
        result.push(entry);
        continue;
      }
      const {
        data,
        partial,
        stale: local_stale,
        hasData
      } = this.getSelection({
        parent: entry,
        selection: fields,
        variables,
        stepsFromConnection,
        ignoreMasking,
        fullCheck,
        loading
      });
      if (data === null && !nullable) {
        cascadeNull = true;
      }
      result.push(data);
      if (partial) {
        partialData = true;
      }
      if (local_stale) {
        stale = true;
      }
      if (hasData) {
        hasValues = true;
      }
    }
    return {
      data: result,
      partial: partialData,
      stale,
      hasData: hasValues,
      cascadeNull
    };
  }
  extractNestedListIDs({
    value,
    abstract,
    recordID,
    key,
    linkedType,
    fields,
    variables,
    applyUpdates,
    specs,
    layer,
    forceNotify
  }) {
    const nestedIDs = [];
    const newIDs = [];
    for (const [i, entry] of value.entries()) {
      if (Array.isArray(entry)) {
        const inner = this.extractNestedListIDs({
          value: entry,
          abstract,
          recordID,
          key,
          linkedType,
          fields,
          variables,
          applyUpdates,
          specs,
          layer,
          forceNotify
        });
        newIDs.push(...inner.newIDs);
        nestedIDs[i] = inner.nestedIDs;
        continue;
      }
      if (entry === null || typeof entry === "undefined") {
        newIDs.push(null);
        nestedIDs[i] = null;
        continue;
      }
      const entryObj = entry;
      let linkedID = `${recordID}.${key}[${this.storage.nextRank}]`;
      let innerType = linkedType;
      const typename = entryObj.__typename;
      if (typename) {
        innerType = typename;
      } else if (abstract) {
        throw new Error("Encountered interface type without __typename in the payload");
      }
      if (!this.isEmbedded(linkedType, entry)) {
        const id = this.id(innerType, entry);
        if (id) {
          linkedID = id;
        } else {
          continue;
        }
      }
      this.writeSelection({
        root: rootID,
        selection: fields,
        parent: linkedID,
        data: entryObj,
        variables,
        toNotify: specs,
        applyUpdates,
        layer,
        forceNotify
      });
      newIDs.push(linkedID);
      nestedIDs[i] = linkedID;
    }
    return { newIDs, nestedIDs };
  }
  collectGarbage() {
    this.lifetimes.tick();
    if (this.storage.layerCount === 1) {
      this.storage.topLayer.removeUndefinedFields();
    }
  }
}
function evaluateVariables(variables, args) {
  return Object.fromEntries(
    Object.entries(variables).map(([key, value]) => [key, variableValue(value, args)])
  );
}
function wrapInLists(target, count = 0) {
  if (count === 0) {
    return target;
  }
  return wrapInLists([target], count - 1);
}
function variableValue(value, args) {
  if (value.kind === "StringValue") {
    return value.value;
  }
  if (value.kind === "BooleanValue") {
    return value.value;
  }
  if (value.kind === "EnumValue") {
    return value.value;
  }
  if (value.kind === "FloatValue") {
    return parseFloat(value.value);
  }
  if (value.kind === "IntValue") {
    return parseInt(value.value, 10);
  }
  if (value.kind === "NullValue") {
    return null;
  }
  if (value.kind === "Variable") {
    return args[value.name.value];
  }
  if (value.kind === "ListValue") {
    return value.values.map((value2) => variableValue(value2, args));
  }
  if (value.kind === "ObjectValue") {
    return value.fields.reduce(
      (obj, field) => ({
        ...obj,
        [field.name.value]: variableValue(field.value, args)
      }),
      {}
    );
  }
}
function fragmentReference({
  component,
  prop
}) {
  return `${component.name}.${prop}`;
}
function defaultComponentField({
  cache,
  component,
  loading,
  variables,
  parent
}) {
  return (props) => {
    const componentFn = cache._internal_unstable.componentCache[component.key];
    const args = evaluateVariables(component.variables ?? {}, variables ?? {});
    return cache._internal_unstable.createComponent(componentFn, {
      ...props,
      [component.prop]: {
        [fragmentKey]: {
          loading,
          values: {
            [component.fragment]: {
              variables: args,
              parent
            }
          }
        }
      }
    });
  };
}
export {
  Cache,
  defaultComponentField,
  evaluateVariables,
  fragmentReference,
  variableValue
};
