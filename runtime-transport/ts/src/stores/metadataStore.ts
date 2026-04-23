import { createStore } from "zustand/vanilla";

import { createRuntimeSessionID, mergeGlobalContextExtras, resolveRuntimeSessionID } from "../runtimeMetadata";

export interface BaseMetadata {
  correlationId: string;
  global_context?: {
    user_id?: string;
    session_id?: string;
    organization_id?: string;
    source?: string;
    extras?: Record<string, unknown>;
    [key: string]: unknown;
  };
  [key: string]: unknown;
}

export interface MetadataState<T extends BaseMetadata = BaseMetadata> {
  metadata: T;
  isInitialized: boolean;
  setMetadata: (meta: Partial<T>) => void;
  updateMetadata: (updater: (current: T) => T) => void;
  setOrganizationId: (organizationId: string | undefined) => void;
  reset: () => void;
}

export const generateCorrelationID = (): string => {
  return `corr_${Date.now()}_${Math.random().toString(36).slice(2, 11)}`;
};

const mergeGlobalContext = (
  current: BaseMetadata["global_context"] | undefined,
  incoming: BaseMetadata["global_context"] | undefined
): BaseMetadata["global_context"] => {
  if (!current && !incoming) {
    return undefined;
  }

  const merged = {
    ...(current ?? {}),
    ...(incoming ?? {}),
  };

  merged.session_id = resolveRuntimeSessionID(incoming?.session_id, current?.session_id);

  const extras = mergeGlobalContextExtras(current?.extras, incoming?.extras ?? {});
  if (Object.keys(extras).length > 0) {
    merged.extras = extras;
  } else {
    delete merged.extras;
  }

  return merged;
};

const createInitialMetadata = (): BaseMetadata => ({
  correlationId: generateCorrelationID(),
  global_context: {
    source: typeof window !== "undefined" ? "frontend" : "backend",
    session_id: createRuntimeSessionID(),
  },
});

const composeMetadata = <T extends BaseMetadata = BaseMetadata>(initialMetaOverride?: Partial<T>): T => {
  const base = createInitialMetadata();
  return {
    ...base,
    ...initialMetaOverride,
    global_context: mergeGlobalContext(base.global_context, initialMetaOverride?.global_context),
    correlationId: initialMetaOverride?.correlationId || base.correlationId,
  } as T;
};

export const createMetadataStore = <T extends BaseMetadata = BaseMetadata>(
  initialMetaOverride?: Partial<T>
) => {
  return createStore<MetadataState<T>>((set) => ({
    metadata: composeMetadata(initialMetaOverride),
    isInitialized: false,

    setMetadata: (newMeta: Partial<T>) => {
      set((state: MetadataState<T>) => {
        const updatedGlobalContext = mergeGlobalContext(state.metadata.global_context, newMeta.global_context);

        return {
          metadata: {
            ...state.metadata,
            ...newMeta,
            global_context: updatedGlobalContext,
            // Preserve correlation ID safety
            correlationId: newMeta.correlationId || state.metadata.correlationId || generateCorrelationID(),
          } as T,
        };
      });
    },

    updateMetadata: (updater: (current: T) => T) => {
      set((state: MetadataState<T>) => {
        const current = {
          ...state.metadata,
          global_context: state.metadata.global_context ? { ...state.metadata.global_context } : undefined,
        } as T;
        const next = updater(current);
        return {
          metadata: {
            ...next,
            global_context: mergeGlobalContext(state.metadata.global_context, next.global_context),
            correlationId: next.correlationId || state.metadata.correlationId || generateCorrelationID(),
          } as T,
        };
      });
    },

    setOrganizationId: (organizationId: string | undefined) => {
      set((state: MetadataState<T>) => {
        const global_context = { ...state.metadata.global_context };
        if (organizationId) {
          global_context.organization_id = organizationId;
        } else {
          delete global_context.organization_id;
        }
        return {
          metadata: {
            ...state.metadata,
            global_context,
          } as T,
        };
      });
    },

    reset: () => {
      set({
        metadata: createInitialMetadata() as T,
        isInitialized: false,
      });
    },
  }));
};
