import { vi } from "vitest";
import { createPluginHooks, HoudiniClient } from "..";
import { ArtifactKind, DataSource } from "../../lib/types";
import { DocumentStore } from "../documentStore";
function createStore(args = {}) {
  if (!args.plugins && !args.pipeline) {
    args.plugins = [fakeFetch({})];
  }
  const client = new HoudiniClient({
    url: "URL",
    ...args
  });
  return new DocumentStore({
    plugins: args.plugins ? createPluginHooks(client.plugins) : void 0,
    pipeline: args.pipeline ? createPluginHooks(client.plugins) : void 0,
    client,
    artifact: args.artifact ?? {
      stripVariables: [],
      kind: ArtifactKind.Query,
      hash: "7777",
      raw: "RAW_TEXT",
      name: "TestArtifact",
      rootType: "Query",
      pluginData: {},
      enableLoadingState: "local",
      selection: {
        fields: {
          viewer: {
            type: "User",
            visible: true,
            keyRaw: "viewer",
            loading: { kind: "continue" },
            selection: {
              fields: {
                id: {
                  type: "ID",
                  visible: true,
                  keyRaw: "id"
                },
                firstName: {
                  type: "String",
                  visible: true,
                  keyRaw: "firstName",
                  loading: { kind: "value" }
                },
                __typename: {
                  type: "String",
                  visible: true,
                  keyRaw: "__typename"
                }
              }
            }
          }
        }
      }
    }
  });
}
function fakeFetch({
  data,
  spy = vi.fn(),
  onRequest
}) {
  const result = {
    data: data ?? {
      viewer: {
        id: "1",
        firstName: "bob",
        __typename: "User"
      }
    },
    errors: null,
    fetching: false,
    variables: null,
    source: DataSource.Network,
    partial: false,
    stale: false
  };
  return (() => ({
    network(ctx, { resolve }) {
      spy?.(ctx);
      if (onRequest) {
        onRequest(ctx.variables ?? {}, () => resolve(ctx, { ...result }));
      } else {
        resolve(ctx, { ...result });
      }
    }
  }));
}
export {
  createStore,
  fakeFetch
};
