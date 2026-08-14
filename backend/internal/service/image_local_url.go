package service

import (
	"fmt"
	"net/url"
	"strings"
)

// JoinLocalObjectURL builds a client-facing absolute URL from a public base and object key.
func JoinLocalObjectURL(baseURL, objectKey string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	objectKey = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(objectKey), `\`, `/`), "/")
	if baseURL == "" {
		return "", fmt.Errorf("local_url is required")
	}
	if objectKey == "" || strings.Contains(objectKey, "..") {
		return "", fmt.Errorf("object key is invalid")
	}
	if strings.HasPrefix(objectKey, "http://") || strings.HasPrefix(objectKey, "https://") {
		u, err := url.Parse(objectKey)
		if err != nil || u.Path == "" || u.Path == "/" {
			return "", fmt.Errorf("cannot derive relative path from object url")
		}
		objectKey = strings.TrimPrefix(u.Path, "/")
	}
	parts := strings.Split(objectKey, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return baseURL + "/" + strings.Join(parts, "/"), nil
}
