package branding

import (
	"encoding/base64"
	"testing"
)

func TestDecodeLogoBytesRawPNG(t *testing.T) {
	raw := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	out, err := DecodeLogoBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !isPNG(out) {
		t.Fatalf("expected png bytes, got %v", out[:8])
	}
}

func TestDecodeLogoBytesBase64PNG(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	encoded := []byte(base64.StdEncoding.EncodeToString(png))
	out, err := DecodeLogoBytes(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !isPNG(out) {
		t.Fatal("expected decoded png")
	}
}

func TestDecodeLogoBytesDataURL(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	out, err := DecodeLogoBytes([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !isPNG(out) {
		t.Fatal("expected decoded png from data url")
	}
}
