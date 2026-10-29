package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBundledPricingSurvivesBinaryOnlyInstall(t *testing.T) {
	data, err := readFallbackPricingFile("./resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Greater(t, len(data), 100)
	_, err = readFallbackPricingFile(filepath.Join(t.TempDir(), "administrator-prices.json"))
	require.Error(t, err)
	file := filepath.Join(t.TempDir(), "prices.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"custom":{}}`), 0600))
	data, err = readFallbackPricingFile(file)
	require.NoError(t, err)
	require.JSONEq(t, `{"custom":{}}`, string(data))
}
