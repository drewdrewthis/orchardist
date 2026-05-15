import { createServerAdapter } from '@whatwg-node/server';
import type { GraphQLSchema } from 'graphql';
import type { HoudiniClient } from '../client';
import { Server } from '../server';
import type { RouterManifest, RouterPageManifest, YogaServerOptions } from './types';
export declare function _serverHandler<ComponentType = unknown>({ schema, server, client, production, manifest, graphqlEndpoint, on_render, componentCache, }: {
    schema?: GraphQLSchema | null;
    server?: Server<any, any>;
    client: HoudiniClient;
    production: boolean;
    manifest: RouterManifest<ComponentType> | null;
    assetPrefix: string;
    graphqlEndpoint: string;
    componentCache: Record<string, any>;
    on_render: (args: {
        url: string;
        match: RouterPageManifest<ComponentType> | null;
        manifest: RouterManifest<unknown>;
        session: App.Session;
        componentCache: Record<string, any>;
    }) => Response | Promise<Response | undefined> | undefined;
} & Omit<YogaServerOptions, 'schema'>): (request: Request, ...extraContext: Array<any>) => Promise<Response>;
export declare const serverAdapterFactory: (args: Parameters<typeof _serverHandler>[0]) => ReturnType<typeof createServerAdapter>;
export type ServerAdapterFactory = typeof serverAdapterFactory;
