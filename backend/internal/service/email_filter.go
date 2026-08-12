package service

import (
	"fmt"
	"net/mail"
	"strings"
)

// NormalizeEmailFilter validates and canonicalizes a semicolon-delimited
// recipient blocklist. Entries are exact mailbox addresses, not patterns.
func NormalizeEmailFilter(value string) (string, error) {
	seen := make(map[string]struct{})
	normalized := make([]string, 0)

	for _, raw := range strings.Split(value, ";") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		address, err := mail.ParseAddress(entry)
		if err != nil || address.Name != "" || !strings.EqualFold(strings.TrimSpace(address.Address), entry) {
			return "", fmt.Errorf("invalid email filter entry: %s", entry)
		}

		canonical := strings.ToLower(strings.TrimSpace(address.Address))
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}

	return strings.Join(normalized, ";"), nil
}

func emailFilterContains(filter, recipient string) bool {
	address, err := mail.ParseAddress(strings.TrimSpace(recipient))
	if err != nil || strings.TrimSpace(address.Address) == "" {
		return false
	}
	recipientAddress := strings.ToLower(strings.TrimSpace(address.Address))

	for _, raw := range strings.Split(filter, ";") {
		if strings.ToLower(strings.TrimSpace(raw)) == recipientAddress {
			return true
		}
	}
	return false
}
