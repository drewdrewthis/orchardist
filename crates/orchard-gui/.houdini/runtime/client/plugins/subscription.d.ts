import type { ClientPluginContext } from '../documentStore';
export declare function subscription(factory: SubscriptionHandler): import("../documentStore").ClientPlugin;
export type SubscriptionHandler = (ctx: ClientPluginContext) => SubscriptionClient;
export type SubscriptionClient = {
    subscribe: (payload: {
        operationName?: string;
        query: string;
        variables?: Record<string, unknown>;
        extensions?: Record<'persistedQuery', string> | Record<string, unknown>;
    }, handlers: {
        next: (payload: {
            data?: {} | null;
            errors?: readonly {
                message: string;
            }[];
        }) => void;
        error: (data: {}) => void;
        complete: () => void;
    }) => () => void;
};
