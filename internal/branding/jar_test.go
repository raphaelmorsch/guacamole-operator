package branding

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestBuildJARTitleOnly(t *testing.T) {
	raw, err := BuildJAR(Options{Title: "Portal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := zipFileNames(t, raw)
	if !contains(files, "guac-manifest.json") || !contains(files, "translations/en.json") {
		t.Fatalf("unexpected jar contents: %v", files)
	}
}

func TestBuildJARWithLogo(t *testing.T) {
	raw, err := BuildJAR(Options{Title: "Portal", HasLogo: true}, []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	files := zipFileNames(t, raw)
	if !contains(files, LogoResourcePath) {
		t.Fatalf("missing logo in jar: %v", files)
	}
}

func zipFileNames(t *testing.T, raw []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
