// Concrete ProjectionProtoCodec backed by the generated foundation.v1 proto
// decoders. frontend-kit defines the codec interface but stays free of a hard
// dependency on the generated transport code; this module bridges the two and is
// wired by the scaffold (which already depends on @ovasabi/runtime-transport).

import { decodeRuntimeEnvelope } from "./binaryEnvelope";
import {
  ProjectionSnapshot,
  RecordMutationBatch,
} from "./generated/foundation/v1/projection";

// Structural shape matching frontend-kit's ProjectionProtoCodec. Kept local so
// runtime-transport does not depend on frontend-kit either; the two meet
// structurally at the scaffold seam.
export type ProjectionProtoCodec = {
  decodeSnapshot(bytes: Uint8Array): ReturnType<typeof ProjectionSnapshot.decode>;
  decodeDeltaFrame(bytes: Uint8Array): ReturnType<typeof RecordMutationBatch.decode>;
};

export const createRuntimeTransportProjectionCodec = (): ProjectionProtoCodec => ({
  decodeSnapshot(bytes: Uint8Array) {
    return ProjectionSnapshot.decode(bytes);
  },
  decodeDeltaFrame(bytes: Uint8Array) {
    const envelope = decodeRuntimeEnvelope(bytes);
    const payload = envelope.payload;
    // Protobuf payloads decode to the raw Uint8Array; the delta frame carries a
    // RecordMutationBatch.
    if (payload instanceof Uint8Array) {
      return RecordMutationBatch.decode(payload);
    }
    if (payload instanceof ArrayBuffer) {
      return RecordMutationBatch.decode(new Uint8Array(payload));
    }
    // Canonical delta frames are protobuf; anything else yields an empty batch.
    return RecordMutationBatch.decode(new Uint8Array());
  },
});
