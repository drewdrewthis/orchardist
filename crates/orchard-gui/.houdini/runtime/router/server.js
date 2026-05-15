import { createServerAdapter } from "@whatwg-node/server";
import { localApiSessionKeys, getCurrentConfig } from "../lib/config";
import { Server } from "../server";
import { serialize as encodeCookie } from "./cookies";
import { find_match } from "./match";
import { get_session, handle_request, session_cookie_name } from "./session";
const config_file = getCurrentConfig();
const session_keys = localApiSessionKeys(config_file);
function _serverHandler({
  schema,
  server,
  client,
  production,
  manifest,
  graphqlEndpoint,
  on_render,
  componentCache
}) {
  if (schema && !server) {
    server = new Server({
      landingPage: !production
    });
  }
  let requestHandler = null;
  if (server && schema) {
    requestHandler = server.init({
      schema,
      endpoint: graphqlEndpoint,
      getSession: (request) => get_session(request.headers, session_keys)
    });
  }
  client.componentCache = componentCache;
  if (requestHandler) {
    client.registerProxy(graphqlEndpoint, async ({ query, variables, session }) => {
      const response = await requestHandler(
        new Request(`http://localhost/${graphqlEndpoint}`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Cookie: encodeCookie(session_cookie_name, JSON.stringify(session ?? {}), {
              httpOnly: true
            })
          },
          body: JSON.stringify({
            query,
            variables
          })
        })
      );
      return await response.json();
    });
  }
  return async (request, ...extraContext) => {
    if (!manifest) {
      return new Response(
        "Adapter did not provide the project's manifest. Please open an issue on github.",
        { status: 500 }
      );
    }
    const url = new URL(request.url).pathname;
    if (requestHandler && url === graphqlEndpoint) {
      return requestHandler(request, ...extraContext);
    }
    const authResponse = await handle_request({
      request,
      config: config_file,
      session_keys
    });
    if (authResponse) {
      return authResponse;
    }
    const [match] = find_match(config_file, manifest, url);
    const rendered = await on_render({
      url,
      match,
      session: await get_session(request.headers, session_keys),
      manifest,
      componentCache
    });
    if (rendered) {
      return rendered;
    }
    return new Response("404", { status: 404 });
  };
}
const serverAdapterFactory = (args) => {
  return createServerAdapter(_serverHandler(args));
};
export {
  _serverHandler,
  serverAdapterFactory
};
