import { flatten } from "../lib/flatten";
import { getFieldsForType } from "../lib/selection";
import { evaluateKey, rootID } from "./stuff";
class InMemorySubscriptions {
  cache;
  constructor(cache) {
    this.cache = cache;
  }
  subscribers = /* @__PURE__ */ new Map();
  keyVersions = {};
  activeFields(parent) {
    return Object.keys(this.subscribers.get(parent) || {});
  }
  copySubscribers(from, to) {
    this.subscribers.set(to, this.subscribers.get(from) || /* @__PURE__ */ new Map());
  }
  add({
    parent,
    spec,
    selection,
    variables,
    parentType
  }) {
    const __typename = this.cache._internal_unstable.storage.get(parent, "__typename").value;
    let targetSelection = getFieldsForType(selection, __typename, false);
    for (const fieldSelection of Object.values(targetSelection || {})) {
      const {
        keyRaw,
        selection: innerSelection,
        type,
        list,
        filters,
        visible
      } = fieldSelection;
      if (!visible) {
        continue;
      }
      const key = evaluateKey(keyRaw, variables);
      let targetSelection2;
      if (innerSelection) {
        const __typename2 = this.cache._internal_unstable.storage.get(parent, "__typename").value;
        targetSelection2 = getFieldsForType(innerSelection, __typename2, false);
      }
      this.addFieldSubscription({
        id: parent,
        key,
        selection: [spec, targetSelection2],
        type
      });
      if (list) {
        this.registerList({
          list,
          filters,
          id: parent,
          key,
          variables,
          selection: innerSelection,
          parentType: parentType || spec.rootType
        });
      }
      if (innerSelection) {
        const { value: linkedRecord } = this.cache._internal_unstable.storage.get(
          parent,
          key
        );
        let children = !Array.isArray(linkedRecord) ? [linkedRecord] : flatten(linkedRecord) || [];
        for (const child of children) {
          if (!child) {
            continue;
          }
          this.add({
            parent: child,
            spec,
            selection: innerSelection,
            variables,
            parentType: type
          });
        }
      }
    }
  }
  addFieldSubscription({
    id,
    key,
    selection,
    type
  }) {
    const spec = selection[0];
    if (!this.subscribers.has(id)) {
      this.subscribers.set(id, /* @__PURE__ */ new Map());
    }
    const subscriber = this.subscribers.get(id);
    if (!subscriber.has(key)) {
      subscriber.set(key, {
        selections: [],
        referenceCounts: /* @__PURE__ */ new Map()
      });
    }
    const subscriberField = subscriber.get(key);
    if (!this.keyVersions[key]) {
      this.keyVersions[key] = /* @__PURE__ */ new Set();
    }
    this.keyVersions[key].add(key);
    if (!subscriberField.selections.some(([{ set }]) => set === spec.set)) {
      subscriberField.selections.push([spec, selection[1]]);
    }
    subscriberField.referenceCounts.set(
      spec.set,
      (subscriberField.referenceCounts.get(spec.set) || 0) + 1
    );
    this.cache._internal_unstable.lifetimes.resetLifetime(id, key);
  }
  registerList({
    list,
    id,
    key,
    parentType,
    selection,
    filters,
    variables
  }) {
    this.cache._internal_unstable.lists.add({
      name: list.name,
      connection: list.connection,
      recordID: id,
      recordType: this.cache._internal_unstable.storage.get(id, "__typename")?.value || parentType,
      listType: list.type,
      key,
      selection,
      filters: Object.entries(filters || {}).reduce((acc, [key2, { kind, value }]) => {
        return {
          ...acc,
          [key2]: kind !== "Variable" ? value : variables[value]
        };
      }, {})
    });
  }
  // this is different from add because of the treatment of lists
  addMany({
    parent,
    variables,
    subscribers,
    parentType
  }) {
    for (const [spec, targetSelection] of subscribers) {
      for (const selection of Object.values(targetSelection ?? {})) {
        const {
          type: linkedType,
          keyRaw,
          selection: innerSelection,
          list,
          filters
        } = selection;
        const key = evaluateKey(keyRaw, variables);
        const fieldSelection = innerSelection ? getFieldsForType(innerSelection, parentType, false) : void 0;
        this.addFieldSubscription({
          id: parent,
          key,
          selection: [spec, fieldSelection],
          type: linkedType
        });
        if (list) {
          this.registerList({
            list,
            filters,
            id: parent,
            key,
            variables,
            selection: innerSelection,
            parentType: parentType || spec.rootType
          });
        }
        const childSelection = selection.selection;
        if (childSelection) {
          const { value: link } = this.cache._internal_unstable.storage.get(parent, key);
          const children = !Array.isArray(link) ? [link] : flatten(link);
          for (const linkedRecord of children) {
            if (!linkedRecord) {
              continue;
            }
            const __typename = this.cache._internal_unstable.storage.get(
              linkedRecord,
              "__typename"
            ).value;
            let targetSelection2 = getFieldsForType(childSelection, __typename, false);
            this.addMany({
              parent: linkedRecord,
              variables,
              subscribers: subscribers.map(([sub]) => [sub, targetSelection2]),
              parentType: linkedType
            });
          }
        }
      }
    }
  }
  get(id, field) {
    return this.subscribers.get(id)?.get(field)?.selections || [];
  }
  getAll(id) {
    return [...this.subscribers.get(id)?.values() || []].flatMap(
      (fieldSub) => fieldSub.selections
    );
  }
  remove(id, selection, targets, variables, visited = []) {
    visited.push(id);
    const linkedIDs = [];
    const __typename = this.cache._internal_unstable.storage.get(id, "__typename").value;
    let targetSelection = getFieldsForType(selection, __typename, false);
    for (const fieldSelection of Object.values(targetSelection || {})) {
      const key = evaluateKey(fieldSelection.keyRaw, variables);
      this.removeSubscribers(id, key, targets);
      if (!fieldSelection.selection) {
        continue;
      }
      const { value: previousValue } = this.cache._internal_unstable.storage.get(id, key);
      const links = !Array.isArray(previousValue) ? [previousValue] : flatten(previousValue);
      for (const link of links) {
        if (link !== null) {
          linkedIDs.push([link, fieldSelection.selection || {}]);
        }
      }
    }
    for (const [linkedRecordID, linkFields] of linkedIDs) {
      this.remove(linkedRecordID, linkFields, targets, visited);
    }
  }
  reset() {
    const subscribers = [...this.subscribers.entries()].filter(([id]) => !id.startsWith(rootID));
    for (const [id, _fields] of subscribers) {
      this.subscribers.delete(id);
    }
    const subscriptionSpecs = subscribers.flatMap(
      ([_id, fields]) => [...fields.values()].flatMap((field) => field.selections.map(([spec]) => spec))
    );
    return subscriptionSpecs;
  }
  removeSubscribers(id, fieldName, specs) {
    let targets = [];
    const subscriber = this.subscribers.get(id);
    if (!subscriber) {
      return;
    }
    const subscriberField = subscriber.get(fieldName);
    for (const spec of specs) {
      const counts = subscriberField?.referenceCounts;
      if (!counts?.has(spec.set)) {
        continue;
      }
      const newVal = (counts.get(spec.set) || 0) - 1;
      counts.set(spec.set, newVal);
      if (newVal <= 0) {
        targets.push(spec.set);
        counts.delete(spec.set);
      }
      if (counts.size === 0) {
        subscriber.delete(fieldName);
      }
    }
    if (subscriberField) {
      subscriberField.selections = this.get(id, fieldName).filter(
        ([{ set }]) => !targets.includes(set)
      );
    }
    if (subscriber.size === 0) {
      this.subscribers.delete(id);
    }
  }
  removeAllSubscribers(id, targets) {
    if (!targets) {
      targets = [...this.subscribers.get(id)?.values() || []].flatMap(
        (spec) => spec.selections.flatMap((sel) => sel[0])
      );
    }
    for (const target of targets) {
      for (const subselection of this.findSubSelections(
        target.parentID || rootID,
        target.selection,
        target.variables || {},
        id
      )) {
        this.remove(id, subselection, targets, target.variables || {});
      }
    }
    return;
  }
  get size() {
    let size = 0;
    for (const [, nodeCounts] of this.subscribers) {
      for (const [, { referenceCounts }] of nodeCounts) {
        size += [...referenceCounts.values()].reduce((size2, count) => size2 + count, 0);
      }
    }
    return size;
  }
  findSubSelections(parentID, selection, variables, searchTarget, selections = []) {
    const __typename = this.cache._internal_unstable.storage.get(parentID, "__typename").value;
    let targetSelection = getFieldsForType(selection, __typename, false);
    for (const fieldSelection of Object.values(targetSelection || {})) {
      if (!fieldSelection.selection) {
        continue;
      }
      const key = evaluateKey(fieldSelection.keyRaw, variables || {});
      const linkedRecord = this.cache._internal_unstable.storage.get(parentID, key);
      const links = !Array.isArray(linkedRecord.value) ? [linkedRecord.value] : flatten(linkedRecord.value);
      if (links.includes(searchTarget)) {
        selections.push(fieldSelection.selection);
      } else {
        for (const link of links) {
          this.findSubSelections(
            link,
            fieldSelection.selection,
            variables,
            searchTarget,
            selections
          );
        }
      }
    }
    return selections;
  }
}
export {
  InMemorySubscriptions
};
