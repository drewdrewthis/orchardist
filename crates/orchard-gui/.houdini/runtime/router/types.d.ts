import type { QueryArtifact } from '$houdini/runtime/lib/types';
import type { createYoga } from 'graphql-yoga';
import type { RouteParam } from './match';
export type YogaServer = ReturnType<typeof createYoga>;
export type YogaServerOptions = Parameters<typeof createYoga>[0];
export type RouterManifest<_ComponentType> = {
    pages: Record<string, RouterPageManifest<_ComponentType>>;
};
export type { ServerAdapterFactory } from './server';
export type RouterPageManifest<_ComponentType> = {
    id: string;
    pattern: RegExp;
    params: RouteParam[];
    documents: Record<string, {
        artifact: () => Promise<{
            default: QueryArtifact;
        }>;
        loading: boolean;
        variables: Record<string, {
            type: string;
        }>;
    }>;
    component: () => Promise<{
        default: _ComponentType;
    }>;
};
