let client = null;
async function initClient() {
  if (client) {
    return client;
  }
  client = (await import("../../../../src/client.ts")).default;
  const delay = (ms) => new Promise((res) => setTimeout(res, ms));
  for (let retry = 0; retry < 10; retry++) {
    if (client) {
      break;
    }
    await delay(100);
    client = (await import("../../../../src/client.ts")).default;
  }
  return client;
}
function getClient() {
  if (!client) {
    throw new Error("client hasn't been initialized");
  }
  return client;
}
export {
  getClient,
  initClient
};
