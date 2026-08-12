//go:build embed

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const htmlCacheTTL = 60 * time.Second

// HTMLCache manages the cached index.html with injected settings
type HTMLCache struct {
	mu              sync.RWMutex
	cachedHTML      []byte
	settingsJSON    []byte
	etag            string
	baseHTMLHash    string // Hash of the original index.html (immutable after build)
	settingsVersion uint64 // Incremented when settings change
	generatedAt     time.Time
}

// CachedHTML represents the cache state
type CachedHTML struct {
	Content      []byte
	SettingsJSON []byte
	ETag         string
}

// NewHTMLCache creates a new HTML cache instance
func NewHTMLCache() *HTMLCache {
	return &HTMLCache{}
}

// SetBaseHTML initializes the cache with the base HTML template
func (c *HTMLCache) SetBaseHTML(baseHTML []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hash := sha256.Sum256(baseHTML)
	c.baseHTMLHash = hex.EncodeToString(hash[:8]) // First 8 bytes for brevity
}

// Invalidate marks the cache as stale
func (c *HTMLCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.settingsVersion++
	c.cachedHTML = nil
	c.settingsJSON = nil
	c.etag = ""
	c.generatedAt = time.Time{}
}

// Get returns the cached HTML or nil if cache is stale
func (c *HTMLCache) Get() *CachedHTML {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.cachedHTML == nil || time.Since(c.generatedAt) >= htmlCacheTTL {
		return nil
	}
	return &CachedHTML{
		Content:      c.cachedHTML,
		SettingsJSON: append([]byte(nil), c.settingsJSON...),
		ETag:         c.etag,
	}
}

// Set updates the cache with new rendered HTML
func (c *HTMLCache) Set(html []byte, settingsJSON []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedHTML = html
	c.settingsJSON = append(c.settingsJSON[:0], settingsJSON...)
	c.etag = c.generateETag(settingsJSON)
	c.generatedAt = time.Now()
}

func (c *HTMLCache) generateAssetETag(settingsJSON []byte, asset string) string {
	hash := sha256.Sum256(append(append([]byte(nil), settingsJSON...), asset...))
	return `"` + c.baseHTMLHash + "-" + hex.EncodeToString(hash[:8]) + `"`
}

// generateETag creates an ETag from base HTML hash + settings hash
func (c *HTMLCache) generateETag(settingsJSON []byte) string {
	settingsHash := sha256.Sum256(settingsJSON)
	return `"` + c.baseHTMLHash + "-" + hex.EncodeToString(settingsHash[:8]) + `"`
}
