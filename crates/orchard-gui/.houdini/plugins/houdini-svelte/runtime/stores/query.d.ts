/// <reference types="svelte" />
import type { FetchContext } from '$houdini/runtime/client/plugins/fetch';
import type { CachePolicies, GraphQLVariables, GraphQLObject, MutationArtifact, QueryArtifact, QueryResult, CursorHandlers, OffsetHandlers, PageInfo } from '$houdini/runtime/lib/types';
import type { Subscriber } from 'svelte/store';
import type { PluginArtifactData } from '../../plugin/artifactData';
import type { ClientFetchParams, LoadEventFetchParams, QueryStoreFetchParams, RequestEventFetchParams } from '../types';
import { BaseStore } from './base';
export declare class QueryStore<_Data extends GraphQLObject, _Input extends GraphQLVariables> extends BaseStore<_Data, _Input, QueryArtifact> {
    variables: boolean;
    kind: "HoudiniQuery";
    protected loadPending: boolean;
    protected storeName: string;
    constructor({ artifact, storeName, variables }: StoreConfig<_Data, _Input, QueryArtifact>);
    /**
     * Fetch the data from the server
     */
    fetch(params?: RequestEventFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: LoadEventFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: ClientFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: QueryStoreFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
}
export type StoreConfig<_Data extends GraphQLObject, _Input, _Artifact> = {
    artifact: _Artifact & {
        pluginData: {
            'houdini-svelte': PluginArtifactData;
        };
    };
    storeName: string;
    variables: boolean;
};
export declare function fetchParams<_Data extends GraphQLObject, _Input>(artifact: QueryArtifact | MutationArtifact, storeName: string, params?: QueryStoreFetchParams<_Data, _Input>): Promise<{
    context: FetchContext;
    policy: CachePolicies | undefined;
    params: QueryStoreFetchParams<_Data, _Input>;
}>;
export type CursorStoreResult<_Data extends GraphQLObject, _Input extends GraphQLVariables> = QueryResult<_Data, _Input> & {
    pageInfo: PageInfo;
};
export declare class QueryStoreCursor<_Data extends GraphQLObject, _Input extends GraphQLVariables> extends QueryStore<_Data, _Input> {
    #private;
    paginated: boolean;
    constructor(config: StoreConfig<_Data, _Input, QueryArtifact>);
    fetch(params?: RequestEventFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: LoadEventFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: ClientFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: QueryStoreFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    loadPreviousPage(args?: Parameters<Required<CursorHandlers<_Data, _Input>>['loadPreviousPage']>[0]): Promise<QueryResult<_Data, _Input>>;
    loadNextPage(args?: Parameters<CursorHandlers<_Data, _Input>['loadNextPage']>[0]): Promise<QueryResult<_Data, _Input>>;
    subscribe(run: Subscriber<CursorStoreResult<_Data, _Input>>, invalidate?: ((value?: CursorStoreResult<_Data, _Input> | undefined) => void) | undefined): () => void;
}
export declare class QueryStoreOffset<_Data extends GraphQLObject, _Input extends GraphQLVariables> extends QueryStore<_Data, _Input> {
    #private;
    paginated: boolean;
    loadNextPage(args?: Parameters<OffsetHandlers<_Data, _Input>['loadNextPage']>[0]): Promise<void>;
    fetch(params?: RequestEventFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: LoadEventFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: ClientFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
    fetch(params?: QueryStoreFetchParams<_Data, _Input>): Promise<QueryResult<_Data, _Input>>;
}
