package branding

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestTitleOnly(t *testing.T) {
	raw, err := Manifest(Options{Title: "My Portal"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["namespace"] != ExtensionNamespace {
		t.Fatalf("namespace: got %v", m["namespace"])
	}
	translations, ok := m["translations"].([]any)
	if !ok || len(translations) != 1 {
		t.Fatalf("expected translations, got %#v", m["translations"])
	}
	if _, ok := m["resources"]; ok {
		t.Fatal("expected no resources without logo")
	}
}

func TestManifestWithLogo(t *testing.T) {
	raw, err := Manifest(Options{HasLogo: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), LogoResourcePath) {
		t.Fatalf("manifest missing logo path: %s", raw)
	}
}

func TestTranslations(t *testing.T) {
	raw, err := Translations("Corp Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Corp Desktop") {
		t.Fatalf("unexpected translations: %s", raw)
	}
}

func TestCSSWithLogo(t *testing.T) {
	css := string(CSS(true))
	if !strings.Contains(css, ExtensionNamespace) || !strings.Contains(css, "background-image") {
		t.Fatalf("unexpected css: %s", css)
	}
	if strings.Contains(css, "height: auto") {
		t.Fatal("logo css must set explicit height")
	}
}
