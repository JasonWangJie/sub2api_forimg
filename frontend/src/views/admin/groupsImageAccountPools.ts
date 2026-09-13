export const IMAGE_SIZE_POOL_TIERS = ["1K", "2K", "4K"] as const;

export type ImageSizePoolTier = (typeof IMAGE_SIZE_POOL_TIERS)[number];

export const IMAGE_ACCOUNT_POOL_MODES = [
  "resolution",
  "model",
  "model_resolution",
] as const;

export type ImageAccountPoolMode = (typeof IMAGE_ACCOUNT_POOL_MODES)[number];

export interface ImageSizePoolBinding {
  account_id: number;
  priority: number;
  account_name?: string | null;
}

export type ImageSizePoolBindingsView = Record<
  ImageSizePoolTier,
  ImageSizePoolBinding[]
>;

export type ImageSizePoolBindingsPayload = Record<
  ImageSizePoolTier,
  Array<{ account_id: number; priority: number }>
>;

export interface ImageModelPoolView {
  model: string;
  accounts: ImageSizePoolBinding[];
}

export interface ImageModelResolutionPoolView {
  model: string;
  resolutions: ImageSizePoolBindingsView;
}

export interface ImageAccountPoolsView {
  mode: ImageAccountPoolMode;
  resolution_pools: ImageSizePoolBindingsView;
  model_pools: ImageModelPoolView[];
  model_resolution_pools: ImageModelResolutionPoolView[];
  model_candidates: string[];
}

export interface ImageAccountPoolsDraft {
  mode: ImageAccountPoolMode;
  resolution_pools: Record<ImageSizePoolTier, string>;
  model_pools: Array<{ model: string; accounts: string }>;
  model_resolution_pools: Array<{
    model: string;
    resolutions: Record<ImageSizePoolTier, string>;
  }>;
  model_candidates: string[];
}

export const supportsImageSizeAccountPools = (platform: string): boolean =>
  platform === "openai" ||
  platform === "gemini" ||
  platform === "antigravity" ||
  platform === "composite";

export const emptyImageSizePoolView = (): ImageSizePoolBindingsView => ({
  "1K": [],
  "2K": [],
  "4K": [],
});

const emptyImageSizePoolDraft = (): Record<ImageSizePoolTier, string> => ({
  "1K": "",
  "2K": "",
  "4K": "",
});

export const emptyImageAccountPoolsView = (): ImageAccountPoolsView => ({
  mode: "resolution",
  resolution_pools: emptyImageSizePoolView(),
  model_pools: [],
  model_resolution_pools: [],
  model_candidates: [],
});

export const normalizeImageSizePoolView = (
  input?: Partial<Record<string, ImageSizePoolBinding[]>> | null,
): ImageSizePoolBindingsView => {
  const view = emptyImageSizePoolView();
  for (const tier of IMAGE_SIZE_POOL_TIERS) {
    const rows = Array.isArray(input?.[tier]) ? input![tier]! : [];
    view[tier] = rows
      .filter((row) => Number(row?.account_id) > 0)
      .map((row, index) => ({
        account_id: Number(row.account_id),
        priority: Number(row.priority) > 0 ? Number(row.priority) : index + 1,
        account_name: row.account_name ?? null,
      }));
  }
  return view;
};

/** Parse "12, 34:2, 56" into ordered bindings (order = priority when omitted). */
export const parseImageSizePoolInput = (
  raw: string,
): Array<{ account_id: number; priority: number }> => {
  const parts = String(raw || "")
    .split(/[,，\s]+/)
    .map((part) => part.trim())
    .filter(Boolean);
  const seen = new Set<number>();
  const out: Array<{ account_id: number; priority: number }> = [];
  for (const part of parts) {
    const [idText, priorityText] = part.split(":");
    const accountId = Number(idText);
    if (!Number.isFinite(accountId) || accountId <= 0 || seen.has(accountId)) {
      continue;
    }
    seen.add(accountId);
    const priority = Number(priorityText);
    out.push({
      account_id: accountId,
      priority: Number.isFinite(priority) && priority > 0 ? priority : out.length + 1,
    });
  }
  return out;
};

export const formatImageSizePoolInput = (
  rows: Array<{ account_id: number; priority?: number; account_name?: string | null }>,
): string => {
  if (!rows?.length) return "";
  return rows
    .map((row) => {
      const accountID = String(row.account_id);
      const priority = Number(row.priority);
      return Number.isFinite(priority) && priority > 0
        ? `${accountID}:${priority}`
        : accountID;
    })
    .join(", ");
};

export const toImageSizePoolPayload = (
  draft: Record<ImageSizePoolTier, string>,
): ImageSizePoolBindingsPayload => ({
  "1K": parseImageSizePoolInput(draft["1K"]),
  "2K": parseImageSizePoolInput(draft["2K"]),
  "4K": parseImageSizePoolInput(draft["4K"]),
});

const normalizeMode = (mode?: string | null): ImageAccountPoolMode =>
  IMAGE_ACCOUNT_POOL_MODES.includes(mode as ImageAccountPoolMode)
    ? (mode as ImageAccountPoolMode)
    : "resolution";

const normalizeExactModel = (model: unknown): string => String(model ?? "").trim();

export const normalizeImageAccountPoolsView = (
  input?: Partial<ImageAccountPoolsView> | null,
): ImageAccountPoolsView => {
  const view = emptyImageAccountPoolsView();
  view.mode = normalizeMode(input?.mode);
  view.resolution_pools = normalizeImageSizePoolView(input?.resolution_pools);
  view.model_candidates = Array.from(
    new Set(
      (Array.isArray(input?.model_candidates) ? input!.model_candidates : [])
        .map(normalizeExactModel)
        .filter(Boolean),
    ),
  );

  const seenModels = new Set<string>();
  for (const pool of Array.isArray(input?.model_pools) ? input!.model_pools : []) {
    const model = normalizeExactModel(pool?.model);
    if (!model || seenModels.has(model)) continue;
    seenModels.add(model);
    view.model_pools.push({
      model,
      accounts: normalizeImageSizePoolView({ "1K": pool.accounts })["1K"],
    });
  }

  const seenCombined = new Set<string>();
  for (const pool of Array.isArray(input?.model_resolution_pools)
    ? input!.model_resolution_pools
    : []) {
    const model = normalizeExactModel(pool?.model);
    if (!model || seenCombined.has(model)) continue;
    seenCombined.add(model);
    view.model_resolution_pools.push({
      model,
      resolutions: normalizeImageSizePoolView(pool.resolutions),
    });
  }
  return view;
};

export const imageAccountPoolsViewToDraft = (
  input?: Partial<ImageAccountPoolsView> | null,
): ImageAccountPoolsDraft => {
  const view = normalizeImageAccountPoolsView(input);
  return {
    mode: view.mode,
    resolution_pools: {
      "1K": formatImageSizePoolInput(view.resolution_pools["1K"]),
      "2K": formatImageSizePoolInput(view.resolution_pools["2K"]),
      "4K": formatImageSizePoolInput(view.resolution_pools["4K"]),
    },
    model_pools: view.model_pools.map((pool) => ({
      model: pool.model,
      accounts: formatImageSizePoolInput(pool.accounts),
    })),
    model_resolution_pools: view.model_resolution_pools.map((pool) => ({
      model: pool.model,
      resolutions: {
        "1K": formatImageSizePoolInput(pool.resolutions["1K"]),
        "2K": formatImageSizePoolInput(pool.resolutions["2K"]),
        "4K": formatImageSizePoolInput(pool.resolutions["4K"]),
      },
    })),
    model_candidates: [...view.model_candidates],
  };
};

export const emptyImageAccountPoolsDraft = (): ImageAccountPoolsDraft =>
  imageAccountPoolsViewToDraft(emptyImageAccountPoolsView());

export const toImageAccountPoolsPayload = (draft: ImageAccountPoolsDraft) => ({
  mode: normalizeMode(draft.mode),
  resolution_pools: toImageSizePoolPayload(draft.resolution_pools),
  model_pools: draft.model_pools
    .map((pool) => ({
      model: normalizeExactModel(pool.model),
      accounts: parseImageSizePoolInput(pool.accounts),
    }))
    .filter((pool) => pool.model),
  model_resolution_pools: draft.model_resolution_pools
    .map((pool) => ({
      model: normalizeExactModel(pool.model),
      resolutions: toImageSizePoolPayload(pool.resolutions),
    }))
    .filter((pool) => pool.model),
});

export const addImageAccountPoolModel = (
  draft: ImageAccountPoolsDraft,
  modelInput: string,
): boolean => {
  const model = normalizeExactModel(modelInput);
  const hasControlCharacter = Array.from(model).some((character) => {
    const codePoint = character.codePointAt(0) ?? 0;
    return codePoint <= 31 || codePoint === 127;
  });
  if (!model || model.includes("*") || hasControlCharacter) {
    return false;
  }
  const target =
    draft.mode === "model" ? draft.model_pools : draft.model_resolution_pools;
  if (target.some((pool) => pool.model === model)) return false;
  if (draft.mode === "model") {
    draft.model_pools.push({ model, accounts: "" });
  } else if (draft.mode === "model_resolution") {
    draft.model_resolution_pools.push({ model, resolutions: emptyImageSizePoolDraft() });
  } else {
    return false;
  }
  if (!draft.model_candidates.includes(model)) draft.model_candidates.push(model);
  return true;
};
