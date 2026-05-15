import { createServerAdapter } from "@whatwg-node/server";
import { YogaServer } from "graphql-yoga";
class Server {
  opts;
  _yoga = null;
  constructor(opts) {
    this.opts = opts ?? null;
  }
  init({
    endpoint,
    schema,
    getSession
  }) {
    this._yoga = new YogaServer({
      ...this.opts,
      schema,
      graphqlEndpoint: endpoint,
      context: async (ctx) => {
        const userContext = !this.opts ? {} : typeof this.opts.context === "function" ? await this.opts.context(ctx) : this.opts.context || {};
        const sessionContext = await getSession(ctx.request) || {};
        return {
          ...userContext,
          session: sessionContext
        };
      }
    });
    return createServerAdapter(this, {
      fetchAPI: this._yoga.fetchAPI,
      plugins: this._yoga["plugins"]
    });
  }
  handle = (request, serverContext) => {
    return this._yoga.handle(request, serverContext);
  };
}
export {
  Server
};
