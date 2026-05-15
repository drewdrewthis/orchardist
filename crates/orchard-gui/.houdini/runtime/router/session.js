import { parse } from "./cookies";
import { decode, encode, verify } from "./jwt";
async function handle_request(args) {
  const plugin_config = args.config.router ?? {};
  const { pathname } = new URL(args.request.url);
  if (plugin_config.auth && "redirect" in plugin_config.auth && pathname.startsWith(plugin_config.auth.redirect)) {
    return await redirect_auth(args);
  }
}
async function redirect_auth(args) {
  if (args.request.method === "GET") {
    const { searchParams } = new URL(
      args.request.url,
      `http://${args.request.headers.get("host")}`
    );
    const { redirectTo, ...session } = Object.fromEntries(searchParams.entries());
    const response = new Response("ok", {
      status: 302,
      headers: {
        Location: redirectTo ?? "/"
      }
    });
    await set_session(args, response, session);
    return response;
  }
  if (args.request.method === "POST") {
    const newValues = await args.request.json();
    const existing = await get_session(args.request.headers, args.session_keys);
    const response = new Response("ok", {
      status: 200
    });
    await set_session(args, response, { ...existing, ...newValues });
    return response;
  }
}
const session_cookie_name = "__houdini__";
async function set_session(req, response, value) {
  const today = /* @__PURE__ */ new Date();
  const expires = new Date(today.getTime() + 7 * 24 * 60 * 60 * 1e3);
  const serialized = await encode(value, req.session_keys[0]);
  response.headers.set(
    "Set-Cookie",
    `${session_cookie_name}=${serialized}; Path=/; HttpOnly; Secure; SameSite=Lax; Expires=${expires.toUTCString()} `
  );
}
async function get_session(req, secrets) {
  const cookies = req.get("cookie");
  if (!cookies) {
    return {};
  }
  const cookie = parse(cookies)[session_cookie_name];
  if (!cookie) {
    return {};
  }
  if (cookie === "{}") {
    return {};
  }
  for (const secret of secrets) {
    if (!await verify(cookie, secret)) {
      continue;
    }
    const parsed = decode(cookie);
    if (!parsed) {
      return {};
    }
    return parsed.payload;
  }
  return {};
}
export {
  get_session,
  handle_request,
  session_cookie_name
};
