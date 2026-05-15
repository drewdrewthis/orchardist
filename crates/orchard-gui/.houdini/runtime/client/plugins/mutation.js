import { marshalSelection } from "../../lib/scalars";
import { ArtifactKind } from "../../lib/types";
import { documentPlugin } from "../utils";
const mutation = (cache) => documentPlugin(ArtifactKind.Mutation, () => {
  return {
    async start(ctx, { next, marshalVariables }) {
      const layerOptimistic = cache._internal_unstable.storage.createLayer(true);
      let toNotify = [];
      const optimisticResponse = ctx.stuff.optimisticResponse;
      if (optimisticResponse) {
        toNotify = cache.write({
          selection: ctx.artifact.selection,
          // make sure that any scalar values get processed into something we can cache
          data: await marshalSelection({
            selection: ctx.artifact.selection,
            data: optimisticResponse
          }),
          variables: marshalVariables(ctx),
          layer: layerOptimistic.id
        });
      }
      ctx.cacheParams = {
        ...ctx.cacheParams,
        // write to the mutation's layer
        layer: layerOptimistic,
        // notify any subscribers that we updated with the optimistic response
        // in order to address situations where the optimistic update was wrong
        notifySubscribers: toNotify,
        // make sure that we notify subscribers for any values that we compare
        // in order to address any race conditions when comparing the previous value
        forceNotify: true
      };
      next(ctx);
    },
    afterNetwork(ctx, { resolve }) {
      if (ctx.cacheParams?.layer) {
        cache.clearLayer(ctx.cacheParams.layer.id);
      }
      resolve(ctx);
    },
    end(ctx, { resolve, value }) {
      const hasErrors = value.errors && value.errors.length > 0;
      if (hasErrors) {
        if (ctx.cacheParams?.layer) {
          cache.clearLayer(ctx.cacheParams.layer.id);
        }
      }
      if (ctx.cacheParams?.layer) {
        cache._internal_unstable.storage.resolveLayer(ctx.cacheParams.layer.id);
      }
      resolve(ctx);
    },
    catch(ctx, { error }) {
      if (ctx.cacheParams?.layer) {
        const { layer } = ctx.cacheParams;
        cache.clearLayer(layer.id);
        cache._internal_unstable.storage.resolveLayer(layer.id);
      }
      throw error;
    }
  };
});
export {
  mutation
};
