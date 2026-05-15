import type { ServerAdapterRequestHandler } from '@whatwg-node/server';
import { YogaServer } from 'graphql-yoga';
import type { YogaSchemaDefinition } from 'graphql-yoga/typings/plugins/use-schema';
type YogaParams = Required<ConstructorParameters<typeof YogaServer>>[0];
type ConstructorParams = Omit<YogaParams, 'schema' | 'graphqlEndpoint'>;
export declare class Server<ServerContext extends Record<string, any>, UserContext extends Record<string, any>> {
    opts: ConstructorParams | null;
    _yoga: YogaServer<any, any> | null;
    constructor(opts?: ConstructorParams);
    init({ endpoint, schema, getSession, }: {
        schema: YogaSchemaDefinition<any, any>;
        endpoint: string;
        getSession: (request: Request) => Promise<UserContext>;
    }): import("@whatwg-node/server").ServerAdapter<ServerContext, Server<ServerContext, UserContext>>;
    handle: ServerAdapterRequestHandler<ServerContext>;
}
export {};
