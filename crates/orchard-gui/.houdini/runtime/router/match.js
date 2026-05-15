import { parseScalar } from "../lib";
const param_pattern = /^(\[)?(\.\.\.)?(\w+)(?:=(\w+))?(\])?$/;
function find_match(config, manifest, current, allowNull = true) {
  let match = null;
  let matchVariables = null;
  for (const page of Object.values(manifest.pages)) {
    const urlMatch = current.match(page.pattern);
    if (!urlMatch) {
      continue;
    }
    match = page;
    matchVariables = exec(urlMatch, page.params) || {};
    break;
  }
  if (!match && !allowNull) {
    throw new Error("404");
  }
  let variables = {
    ...matchVariables
  };
  for (const document of Object.values(match?.documents ?? {})) {
    for (const [variable, { type }] of Object.entries(document.variables)) {
      if (matchVariables?.[variable]) {
        variables[variable] = parseScalar(config, type, matchVariables[variable]);
      }
    }
  }
  return [match, variables];
}
function parse_page_pattern(id) {
  const params = [];
  const pattern = id === "/" ? /^\/$/ : new RegExp(
    `^${get_route_segments(id).map((segment) => {
      const rest_match = /^\[\.\.\.(\w+)(?:=(\w+))?\]$/.exec(segment);
      if (rest_match) {
        params.push({
          name: rest_match[1],
          matcher: rest_match[2],
          optional: false,
          rest: true,
          chained: true
        });
        return "(?:/(.*))?";
      }
      const optional_match = /^\[\[(\w+)(?:=(\w+))?\]\]$/.exec(segment);
      if (optional_match) {
        params.push({
          name: optional_match[1],
          matcher: optional_match[2],
          optional: true,
          rest: false,
          chained: true
        });
        return "(?:/([^/]+))?";
      }
      if (!segment) {
        return;
      }
      const parts = segment.split(/\[(.+?)\](?!\])/);
      const result = parts.map((content, i) => {
        if (i % 2) {
          if (content.startsWith("x+")) {
            return escape(
              String.fromCharCode(parseInt(content.slice(2), 16))
            );
          }
          if (content.startsWith("u+")) {
            return escape(
              String.fromCharCode(
                ...content.slice(2).split("-").map((code) => parseInt(code, 16))
              )
            );
          }
          const match = param_pattern.exec(content);
          if (!match) {
            throw new Error(
              `Invalid param: ${content}. Params and matcher names can only have underscores and alphanumeric characters.`
            );
          }
          const [, is_optional, is_rest, name, matcher] = match;
          params.push({
            name,
            matcher,
            optional: !!is_optional,
            rest: !!is_rest,
            chained: is_rest ? i === 1 && parts[0] === "" : false
          });
          return is_rest ? "(.*?)" : is_optional ? "([^/]*)?" : "([^/]+?)";
        }
        return escape(content);
      }).join("");
      return "/" + result;
    }).join("")}/?$`
  );
  return { pattern, params, page_id: id };
}
function affects_path(segment) {
  return !/^\([^)]+\)$/.test(segment);
}
function get_route_segments(route) {
  return route.slice(1).split("/").filter(affects_path);
}
function exec(match, params) {
  const result = {};
  const values = match.slice(1);
  let buffered = "";
  for (let i = 0; i < (params || []).length; i += 1) {
    const param = params[i];
    let value = values[i];
    if (param.chained && param.rest && buffered) {
      value = value ? buffered + "/" + value : buffered;
    }
    buffered = "";
    if (value === void 0) {
      if (param.rest) result[param.name] = "";
    } else {
      result[param.name] = decodeURIComponent(value);
    }
  }
  if (buffered) return;
  return result;
}
function escape(str) {
  return str.normalize().replace(/[[\]]/g, "\\$&").replace(/%/g, "%25").replace(/\//g, "%2[Ff]").replace(/\?/g, "%3[Ff]").replace(/#/g, "%23").replace(/[.*+?^${}()|\\]/g, "\\$&");
}
export {
  exec,
  find_match,
  get_route_segments,
  parse_page_pattern
};
