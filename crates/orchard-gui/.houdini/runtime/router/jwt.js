function base64UrlParse(s) {
  return new Uint8Array(
    // @ts-ignore
    Array.prototype.map.call(
      atob(s.replace(/-/g, "+").replace(/_/g, "/").replace(/\s/g, "")),
      (c) => c.charCodeAt(0)
    )
  );
}
function base64UrlStringify(a) {
  return btoa(String.fromCharCode.apply(0, a)).replace(/=/g, "").replace(/\+/g, "-").replace(/\//g, "_");
}
const algorithms = {
  ES256: { name: "ECDSA", namedCurve: "P-256", hash: { name: "SHA-256" } },
  ES384: { name: "ECDSA", namedCurve: "P-384", hash: { name: "SHA-384" } },
  ES512: { name: "ECDSA", namedCurve: "P-521", hash: { name: "SHA-512" } },
  HS256: { name: "HMAC", hash: { name: "SHA-256" } },
  HS384: { name: "HMAC", hash: { name: "SHA-384" } },
  HS512: { name: "HMAC", hash: { name: "SHA-512" } },
  RS256: { name: "RSASSA-PKCS1-v1_5", hash: { name: "SHA-256" } },
  RS384: { name: "RSASSA-PKCS1-v1_5", hash: { name: "SHA-384" } },
  RS512: { name: "RSASSA-PKCS1-v1_5", hash: { name: "SHA-512" } }
};
function _utf8ToUint8Array(str) {
  return base64UrlParse(btoa(unescape(encodeURIComponent(str))));
}
function _str2ab(str) {
  str = atob(str);
  const buf = new ArrayBuffer(str.length);
  const bufView = new Uint8Array(buf);
  for (let i = 0, strLen = str.length; i < strLen; i++) {
    bufView[i] = str.charCodeAt(i);
  }
  return buf;
}
function _decodePayload(raw) {
  switch (raw.length % 4) {
    case 0:
      break;
    case 2:
      raw += "==";
      break;
    case 3:
      raw += "=";
      break;
    default:
      throw new Error("Illegal base64url string!" + raw);
  }
  try {
    return JSON.parse(decodeURIComponent(escape(atob(raw))));
  } catch {
    return null;
  }
}
async function encode(payload, secret, options = { algorithm: "HS256", header: { typ: "JWT" } }) {
  if (typeof options === "string") options = { algorithm: options, header: { typ: "JWT" } };
  options = { algorithm: "HS256", header: { typ: "JWT" }, ...options };
  if (payload === null || typeof payload !== "object")
    throw new Error("payload must be an object");
  if (typeof secret !== "string" && typeof secret !== "object")
    throw new Error("secret must be a string or a JWK object");
  if (typeof options.algorithm !== "string") throw new Error("options.algorithm must be a string");
  const algorithm = algorithms[options.algorithm];
  if (!algorithm) throw new Error("algorithm not found");
  if (!payload.iat) payload.iat = Math.floor(Date.now() / 1e3);
  const payloadAsJSON = JSON.stringify(payload);
  const partialToken = `${base64UrlStringify(
    _utf8ToUint8Array(JSON.stringify({ ...options.header, alg: options.algorithm }))
  )}.${base64UrlStringify(_utf8ToUint8Array(payloadAsJSON))}`;
  let keyFormat = "raw";
  let keyData;
  if (typeof secret === "object") {
    keyFormat = "jwk";
    keyData = secret;
  } else if (typeof secret === "string" && secret.startsWith("-----BEGIN")) {
    keyFormat = "pkcs8";
    keyData = _str2ab(
      secret.replace(/-----BEGIN.*?-----/g, "").replace(/-----END.*?-----/g, "").replace(/\s/g, "")
    );
  } else keyData = _utf8ToUint8Array(secret);
  const key = await crypto.subtle.importKey(keyFormat, keyData, algorithm, false, ["sign"]);
  const signature = await crypto.subtle.sign(algorithm, key, _utf8ToUint8Array(partialToken));
  return `${partialToken}.${base64UrlStringify(new Uint8Array(signature))}`;
}
async function verify(token, secret, options = { algorithm: "HS256", throwError: false }) {
  if (typeof options === "string") options = { algorithm: options, throwError: false };
  options = { algorithm: "HS256", throwError: false, ...options };
  if (typeof token !== "string") throw new Error("token must be a string");
  if (typeof secret !== "string" && typeof secret !== "object")
    throw new Error("secret must be a string or a JWK object");
  if (typeof options.algorithm !== "string") throw new Error("options.algorithm must be a string");
  const tokenParts = token.split(".");
  if (tokenParts.length !== 3) throw new Error("token must consist of 3 parts");
  const algorithm = algorithms[options.algorithm];
  if (!algorithm) throw new Error("algorithm not found");
  const { payload } = decode(token);
  if (!payload) {
    if (options.throwError) throw "PARSE_ERROR";
    return false;
  }
  if (payload.nbf && payload.nbf > Math.floor(Date.now() / 1e3)) {
    if (options.throwError) throw "NOT_YET_VALID";
    return false;
  }
  if (payload.exp && payload.exp <= Math.floor(Date.now() / 1e3)) {
    if (options.throwError) throw "EXPIRED";
    return false;
  }
  let keyFormat = "raw";
  let keyData;
  if (typeof secret === "object") {
    keyFormat = "jwk";
    keyData = secret;
  } else if (typeof secret === "string" && secret.startsWith("-----BEGIN")) {
    keyFormat = "spki";
    keyData = _str2ab(
      secret.replace(/-----BEGIN.*?-----/g, "").replace(/-----END.*?-----/g, "").replace(/\s/g, "")
    );
  } else keyData = _utf8ToUint8Array(secret);
  const key = await crypto.subtle.importKey(keyFormat, keyData, algorithm, false, ["verify"]);
  return await crypto.subtle.verify(
    algorithm,
    key,
    base64UrlParse(tokenParts[2]),
    _utf8ToUint8Array(`${tokenParts[0]}.${tokenParts[1]}`)
  );
}
function decode(token) {
  return {
    header: _decodePayload(
      token.split(".")[0].replace(/-/g, "+").replace(/_/g, "/")
    ),
    payload: _decodePayload(
      token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")
    )
  };
}
export {
  decode,
  encode,
  verify
};
