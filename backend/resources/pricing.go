package resources

import _ "embed"

// ModelPricing is shipped with binary installations as well as containers.
// Administrators can still override pricing with an explicit external file.
//
//go:embed model-pricing/model_prices_and_context_window.json
var ModelPricing []byte
