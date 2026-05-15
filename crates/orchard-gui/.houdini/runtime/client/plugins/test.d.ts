import { type HoudiniClientConstructorArgs } from '..';
import type { DocumentArtifact, GraphQLObject } from '../../lib';
import type { ClientPlugin, ClientPluginContext } from '../documentStore';
import { DocumentStore } from '../documentStore';
/**
 * Utilities for testing the cache plugin
 */
export declare function createStore(args?: Partial<HoudiniClientConstructorArgs> & {
    artifact?: DocumentArtifact;
}): DocumentStore<any, any>;
export declare function fakeFetch({ data, spy, onRequest, }: {
    data?: any;
    spy?: (ctx: ClientPluginContext) => void;
    onRequest?: (variables: GraphQLObject, cb: () => void) => void;
}): ClientPlugin;
