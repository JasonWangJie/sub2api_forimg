import { describe, expect, it } from "vitest";
import {
  emptyImageSizePoolView,
  formatImageSizePoolInput,
  normalizeImageSizePoolView,
  parseImageSizePoolInput,
  supportsImageSizeAccountPools,
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
});
