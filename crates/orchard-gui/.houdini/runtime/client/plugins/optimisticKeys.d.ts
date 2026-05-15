import type { Cache } from '../../cache/cache';
import type { ClientPlugin } from '../documentStore';
export type CallbackMap = Record<string | number, Array<(newID: any) => void>>;
export type KeyMap = Record<number, Record<string, keyof CallbackMap>>;
type OptimisticObjectIDMap = Record<number, Record<string, string>>;
export declare const optimisticKeys: (cache: Cache, callbackCache?: CallbackMap, keyCache?: KeyMap, objectIDs?: OptimisticObjectIDMap, invocationCounter?: number) => ClientPlugin;
export {};
