import configFile from "../../imports/config";
import { computeID, getFieldsForType, keyFieldsForType, marshalSelection } from "../../lib";
import { ArtifactKind } from "../../lib/types";
const keys = {};
const callbacks = {};
const objectIDMap = {};
const optimisticKeys = (cache, callbackCache = callbacks, keyCache = keys, objectIDs = objectIDMap, invocationCounter = 1) => () => {
  return {
    async start(ctx, { next }) {
      const optimisticResponse = ctx.stuff.optimisticResponse;
      const newCtx = { ...ctx };
      if (optimisticResponse && ctx.artifact.kind === ArtifactKind.Mutation && ctx.artifact.optimisticKeys) {
        newCtx.stuff.mutationID = invocationCounter++;
        addKeysToResponse({
          selection: ctx.artifact.selection,
          response: optimisticResponse,
          callbackStore: callbackCache,
          keyStore: keyCache,
          objectIDs,
          mutationID: newCtx.stuff.mutationID
        });
        newCtx.stuff.optimisticResponse = optimisticResponse;
      }
      next(newCtx);
    },
    // if a request has variables that contain an optimistic key we need to block the
    // request before it is sent to the server
    beforeNetwork(ctx, { next }) {
      if (Object.keys(keyCache).length === 0) {
        return next(ctx);
      }
      const pendingVariables = extractInputKeys(
        ctx.variables ?? {},
        callbackCache
      );
      if (Object.keys(pendingVariables).length === 0) {
        return next(ctx);
      }
      for (const key of Object.keys(pendingVariables)) {
        callbackCache[key].push((newID) => {
          pendingVariables[key] = newID;
          if (Object.values(pendingVariables).every((value) => value !== null)) {
            next({
              ...ctx,
              variables: replaceKeyWithVariable(
                { ...ctx.variables },
                pendingVariables
              )
            });
          }
        });
      }
    },
    afterNetwork(ctx, { value, resolve }) {
      if (ctx.artifact.kind === ArtifactKind.Mutation && ctx.artifact.optimisticKeys && typeof ctx.stuff.mutationID !== "undefined") {
        extractResponseKeys(
          cache,
          value.data ?? {},
          ctx.artifact.selection,
          keyCache,
          ctx.stuff.mutationID,
          {
            onNewKey: (optimisticValue, realValue) => {
              callbackCache[optimisticValue].forEach((cb) => {
                cb(realValue);
              });
              delete callbackCache[optimisticValue];
            },
            onIDChange: (optimisticValue, realValue) => {
              cache.registerKeyMap(optimisticValue, realValue);
            }
          }
        );
      }
      resolve(ctx);
    },
    // when the mutation ends, we no longer have any dependents that we have to track
    end(ctx, { resolve }) {
      if (typeof ctx.stuff.mutationID !== "undefined") {
        delete keyCache[ctx.stuff.mutationID];
        delete objectIDs[ctx.stuff.mutationID];
      }
      resolve(ctx);
    }
  };
};
function addKeysToResponse(args) {
  let targetSelection = getFieldsForType(
    args.selection,
    args.response["__typename"],
    false
  );
  const newKeys = [];
  for (const [field, { type, selection: fieldSelection, optimisticKey }] of Object.entries(
    targetSelection
  )) {
    const value = args.response[field];
    const pathSoFar = `${args.path ?? ""}.${field}`;
    if (optimisticKey) {
      let keyValue;
      if (value) {
        const { marshaled } = marshalSelection({
          data: { marshaled: value },
          selection: {
            fields: {
              value: {
                type,
                keyRaw: "value"
              }
            }
          }
        });
        keyValue = marshaled;
      } else {
        keyValue = generateKey(type);
      }
      newKeys.push(keyValue);
      args.response[field] = keyValue;
      args.callbackStore[keyValue] = [];
      args.keyStore[args.mutationID] = {
        [pathSoFar]: keyValue
      };
    }
    if (fieldSelection) {
      if (Array.isArray(value)) {
        for (const [index, item] of flattenList(value).entries()) {
          if (item && typeof item === "object" && !Array.isArray(item)) {
            addKeysToResponse({
              ...args,
              selection: fieldSelection,
              response: item,
              type,
              path: `${pathSoFar}[${index}]`
            });
          }
        }
      } else if (value && typeof value == "object") {
        addKeysToResponse({
          ...args,
          selection: fieldSelection,
          response: value,
          type,
          path: pathSoFar
        });
      }
    }
  }
  if (newKeys.length > 0) {
    const objID = `${args.type}:${computeID(configFile, args.type ?? "", args.response)}`;
    for (const key of newKeys) {
      args.objectIDs[args.mutationID] = {
        ...args.objectIDs[args.mutationID],
        [key]: objID
      };
    }
  }
  return args.response;
}
function extractInputKeys(obj, store, found = {}) {
  for (const value of Object.values(obj)) {
    if (typeof value === "string" && store[value]) {
      found[value] = null;
    }
    if (Array.isArray(value)) {
      for (const item of flattenList(value)) {
        if (item && typeof item === "object") {
          extractInputKeys(item, store, found);
        }
      }
    } else if (value && typeof value === "object") {
      extractInputKeys(value, store, found);
    }
  }
  return found;
}
function extractResponseKeys(cache, response, selection, keyMap, mutationID, events, objectIDs = objectIDMap, path = "", type = "") {
  let targetSelection = getFieldsForType(
    selection,
    response["__typename"],
    false
  );
  let optimisticID = null;
  for (const [field, value] of Object.entries(response)) {
    const pathSoFar = `${path ?? ""}.${field}`;
    if (typeof value === "string" && keyMap[mutationID][pathSoFar]) {
      const newKey = keyMap[mutationID][pathSoFar];
      events.onNewKey(newKey, value);
      optimisticID = objectIDs[mutationID][newKey];
    }
    if (!selection || !targetSelection[field]) {
      continue;
    }
    let { type: type2, selection: fieldSelection } = targetSelection[field];
    if (Array.isArray(value)) {
      for (const [index, item] of flattenList(value).entries()) {
        if (item && typeof item === "object" && fieldSelection) {
          extractResponseKeys(
            cache,
            item,
            fieldSelection,
            keyMap,
            mutationID,
            events,
            objectIDs,
            `${pathSoFar}[${index}]`,
            type2
          );
        }
      }
    } else if (value && typeof value === "object" && fieldSelection) {
      extractResponseKeys(
        cache,
        value,
        fieldSelection,
        keyMap,
        mutationID,
        events,
        objectIDs,
        pathSoFar,
        type2
      );
    }
  }
  if (optimisticID) {
    const id = computeID(configFile, type, response);
    events.onIDChange(`${type}:${id}`, optimisticID);
    cache.write({
      selection: {
        fields: Object.fromEntries(
          keyFieldsForType(configFile, type).map((key) => [
            key,
            {
              type: "scalar",
              keyRaw: key
            }
          ])
        )
      },
      parent: optimisticID,
      data: response
    });
  }
}
function flattenList(source) {
  const result = [];
  const left = [...source];
  while (left.length > 0) {
    const head = left.shift();
    if (Array.isArray(head)) {
      left.push(...head);
    } else {
      result.push(head);
    }
  }
  return result;
}
function replaceKeyWithVariable(variables, keys2) {
  for (const [key, value] of Object.entries(variables)) {
    if (typeof value === "string" && keys2[value]) {
      variables[key] = keys2[value];
    }
    if (Array.isArray(value)) {
      for (const item of flattenList(value)) {
        if (item && typeof item === "object") {
          replaceKeyWithVariable(item, keys2);
        }
      }
    } else if (value && typeof value === "object") {
      replaceKeyWithVariable(value, keys2);
    }
  }
  return variables;
}
function generateKey(type) {
  if (type === "Int") {
    return (/* @__PURE__ */ new Date()).getTime();
  }
  if (type === "String") {
    return (/* @__PURE__ */ new Date()).getTime().toString();
  }
  if (type === "ID") {
    return (/* @__PURE__ */ new Date()).getTime().toString();
  }
  throw new Error(
    `unsupported type for optimistic key: ${type}. Please provide a value in your mutation arguments.`
  );
}
export {
  optimisticKeys
};
