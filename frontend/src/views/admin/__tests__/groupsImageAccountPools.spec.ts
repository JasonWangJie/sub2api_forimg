import { describe, expect, it } from "vitest";
import {
  addImageAccountPoolModel,
  emptyImageAccountPoolsDraft,
  emptyImageSizePoolView,
  formatImageSizePoolInput,
  imageAccountPoolsViewToDraft,
  normalizeImageAccountPoolsView,
  normalizeImageSizePoolView,
  parseImageSizePoolInput,
  supportsImageSizeAccountPools,
  toImageAccountPoolsPayload,
  toImageSizePoolPayload,
} from "../groupsImageAccountPools";

describe("groupsImageAccountPools", () => {
  it("supports openai/gemini/antigravity/composite only", () => {
    expect(supportsImageSizeAccountPools("openai")).toBe(true);
    expect(supportsImageSizeAccountPools("gemini")).toBe(true);
    expect(supportsImageSizeAccountPools("anthropic")).toBe(false);
  });

  it("parses ordered account ids and optional priorities", () => {
    expect(parseImageSizePoolInput("101, 102:5, 101, abc")).toEqual([
      { account_id: 101, priority: 1 },
      { account_id: 102, priority: 5 },
    ]);
  });

  it("round-trips empty and configured tiers", () => {
    const view = normalizeImageSizePoolView({
      "4K": [
        { account_id: 9, priority: 1, account_name: "a" },
        { account_id: 8, priority: 1, account_name: "b" },
      ],
    });
    expect(view["1K"]).toEqual([]);
    expect(view["4K"][0].account_id).toBe(9);
    expect(formatImageSizePoolInput(view["4K"])).toBe("9:1, 8:1");
    expect(parseImageSizePoolInput(formatImageSizePoolInput(view["4K"]))).toEqual([
      { account_id: 9, priority: 1 },
      { account_id: 8, priority: 1 },
    ]);
    expect(toImageSizePoolPayload({ "1K": "", "2K": "", "4K": "9,8" })).toEqual({
      "1K": [],
      "2K": [],
      "4K": [
        { account_id: 9, priority: 1 },
        { account_id: 8, priority: 2 },
      ],
    });
    expect(emptyImageSizePoolView()["2K"]).toEqual([]);
  });

  it("round-trips all three independent pool sets and priorities", () => {
    const draft = imageAccountPoolsViewToDraft(
      normalizeImageAccountPoolsView({
        mode: "model_resolution",
        resolution_pools: {
          "1K": [{ account_id: 1, priority: 7 }],
          "2K": [],
          "4K": [],
        },
        model_pools: [
          {
            model: "gpt-image-2",
            accounts: [{ account_id: 2, priority: 3 }],
          },
        ],
        model_resolution_pools: [
          {
            model: "gpt-image-2.5-sunburst",
            resolutions: {
              "1K": [],
              "2K": [{ account_id: 3, priority: 1 }],
              "4K": [],
            },
          },
        ],
        model_candidates: ["gpt-image-2", "gpt-image-2"],
      }),
    );

    expect(draft.mode).toBe("model_resolution");
    expect(draft.resolution_pools["1K"]).toBe("1:7");
    expect(draft.model_pools[0].accounts).toBe("2:3");
    expect(draft.model_candidates).toEqual(["gpt-image-2"]);
    expect(toImageAccountPoolsPayload(draft)).toMatchObject({
      mode: "model_resolution",
      resolution_pools: { "1K": [{ account_id: 1, priority: 7 }] },
      model_pools: [
        {
          model: "gpt-image-2",
          accounts: [{ account_id: 2, priority: 3 }],
        },
      ],
      model_resolution_pools: [
        {
          model: "gpt-image-2.5-sunburst",
          resolutions: { "2K": [{ account_id: 3, priority: 1 }] },
        },
      ],
    });
  });

  it("adds exact models only to the active model mode and de-duplicates", () => {
    const draft = emptyImageAccountPoolsDraft();
    draft.mode = "model";
    expect(addImageAccountPoolModel(draft, " gpt-image-2 ")).toBe(true);
    expect(addImageAccountPoolModel(draft, "gpt-image-2")).toBe(false);
    expect(addImageAccountPoolModel(draft, "gpt-image-*")).toBe(false);
    expect(draft.model_pools).toEqual([{ model: "gpt-image-2", accounts: "" }]);

    draft.mode = "model_resolution";
    expect(addImageAccountPoolModel(draft, "gpt-image-2")).toBe(true);
    expect(draft.model_resolution_pools[0].resolutions).toEqual({
      "1K": "",
      "2K": "",
      "4K": "",
    });
    expect(draft.model_pools).toHaveLength(1);
  });
});
