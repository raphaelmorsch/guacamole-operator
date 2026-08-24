package branding

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
)

// DecodeLogoBytes returns raw image bytes from a Secret/ConfigMap value.
// Kubernetes ConfigMap data is often base64 text when the image was pasted via the console.
func DecodeLogoBytes(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("logo image is empty")
	}
	if isPNG(raw) || isJPEG(raw) {
		return raw, nil
	}

	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "data:") {
		if idx := strings.Index(trimmed, ","); idx >= 0 {
			trimmed = trimmed[idx+1:]
		}
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("logo image is not valid PNG/JPEG data or base64: %w", err)
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("decoded logo image is empty")
	}
	if !isPNG(decoded) && !isJPEG(decoded) {
		return nil, fmt.Errorf("decoded logo is not a supported PNG or JPEG image")
	}
	return decoded, nil
}

func isPNG(data []byte) bool {
	return len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
}

func isJPEG(data []byte) bool {
	return len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
}
