import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";

import ImageConcurrencySettingsCard from "../ImageConcurrencySettingsCard.vue";

const {
  getImageConcurrencySettings,
  updateImageConcurrencySettings,
  resetImageConcurrencySettings,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  getImageConcurrencySettings: vi.fn(),
  updateImageConcurrencySettings: vi.fn(),
  resetImageConcurrencySettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

const yamlView = {
  configured: false,
  source: "config_yaml" as const,
  effective: {
    enabled: true,
    max_concurrent_requests: 50,
    overflow_mode: "wait" as const,
    wait_timeout_seconds: 360,
    max_waiting_requests: 120,
  },
  fallback: {
    enabled: true,
    max_concurrent_requests: 50,
    overflow_mode: "wait" as const,
    wait_timeout_seconds: 360,
    max_waiting_requests: 120,
  },
};

vi.mock("@/api", () => ({
  adminAPI: {
    settings: {
      getImageConcurrencySettings,
      updateImageConcurrencySettings,
      resetImageConcurrencySettings,
    },
  },
}));

vi.mock("@/stores", () => ({
  useAppStore: () => ({ showError, showSuccess }),
}));

vi.mock("@/utils/apiError", () => ({
  extractApiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}));

describe("ImageConcurrencySettingsCard", () => {
  beforeEach(() => {
    getImageConcurrencySettings.mockReset();
    updateImageConcurrencySettings.mockReset();
    resetImageConcurrencySettings.mockReset();
    showError.mockReset();
    showSuccess.mockReset();
    getImageConcurrencySettings.mockResolvedValue(yamlView);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads the effective YAML fallback and saves a system-settings override", async () => {
    updateImageConcurrencySettings.mockImplementation(async (payload) => ({
      configured: true,
      source: "system_settings",
      effective: payload,
      fallback: yamlView.fallback,
    }));
    const wrapper = mount(ImageConcurrencySettingsCard);
    await flushPromises();

    expect(wrapper.get('[data-testid="image-concurrency-source"]').text()).toContain(
      "admin.settings.imageConcurrency.sourceYaml",
    );
    const maxInput = wrapper.get('[data-testid="image-concurrency-max"]');
    expect((maxInput.element as HTMLInputElement).value).toBe("50");

    await maxInput.setValue("200");
    await wrapper.get('[data-testid="image-concurrency-mode"]').setValue("reject");
    await wrapper.get('[data-testid="image-concurrency-save"]').trigger("click");
    await flushPromises();

    expect(updateImageConcurrencySettings).toHaveBeenCalledWith({
      enabled: true,
      max_concurrent_requests: 200,
      overflow_mode: "reject",
      wait_timeout_seconds: 360,
      max_waiting_requests: 120,
    });
    expect(wrapper.get('[data-testid="image-concurrency-source"]').text()).toContain(
      "admin.settings.imageConcurrency.sourceSystem",
    );
    expect(showSuccess).toHaveBeenCalledWith(
      "admin.settings.imageConcurrency.saveSuccess",
    );
  });

  it("deletes the override and restores config.yaml after confirmation", async () => {
    getImageConcurrencySettings.mockResolvedValue({
      ...yamlView,
      configured: true,
      source: "system_settings",
      effective: { ...yamlView.effective, max_concurrent_requests: 200 },
    });
    resetImageConcurrencySettings.mockResolvedValue(yamlView);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const wrapper = mount(ImageConcurrencySettingsCard);
    await flushPromises();

    const resetButton = wrapper.findAll("button").find((button) =>
      button.text().includes("admin.settings.imageConcurrency.reset"),
    );
    expect(resetButton).toBeDefined();
    await resetButton!.trigger("click");
    await flushPromises();

    expect(resetImageConcurrencySettings).toHaveBeenCalledOnce();
    expect((wrapper.get('[data-testid="image-concurrency-max"]').element as HTMLInputElement).value).toBe("50");
    expect(showSuccess).toHaveBeenCalledWith(
      "admin.settings.imageConcurrency.resetSuccess",
    );
  });
});
