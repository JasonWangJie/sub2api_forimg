package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/resources"
)

func readFallbackPricingFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	// Only the bundled default can fall back to embedded data. A misspelled
	// administrator-supplied file must remain a visible configuration error.
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if os.IsNotExist(err) && clean == "resources/model-pricing/model_prices_and_context_window.json" {
		return resources.ModelPricing, nil
	}
	return nil, err
}
