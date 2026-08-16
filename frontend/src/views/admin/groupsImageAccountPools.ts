export const IMAGE_SIZE_POOL_TIERS = ["1K", "2K", "4K"] as const;

export type ImageSizePoolTier = (typeof IMAGE_SIZE_POOL_TIERS)[number];

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
  return rows.map((row) => String(row.account_id)).join(", ");
};

export const toImageSizePoolPayload = (
  draft: Record<ImageSizePoolTier, string>,
): ImageSizePoolBindingsPayload => ({
  "1K": parseImageSizePoolInput(draft["1K"]),
  "2K": parseImageSizePoolInput(draft["2K"]),
  "4K": parseImageSizePoolInput(draft["4K"]),
});
