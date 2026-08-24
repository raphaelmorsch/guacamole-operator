package branding

import (
	"archive/zip"
	"bytes"
	"fmt"
)

// BuildJAR packages the Guacamole branding extension as a JAR (zip) archive.
func BuildJAR(opts Options, logoPNG []byte) ([]byte, error) {
	if opts.HasLogo && len(logoPNG) == 0 {
		return nil, fmt.Errorf("logo image is required when HasLogo is true")
	}

	manifest, err := Manifest(opts)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}

	var translations []byte
	if opts.Title != "" {
		translations, err = Translations(opts.Title)
		if err != nil {
			return nil, fmt.Errorf("translations: %w", err)
		}
	}

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	files := map[string][]byte{
		"guac-manifest.json": manifest,
		"css/branding.css":   CSS(opts.HasLogo),
	}
	if len(translations) > 0 {
		files["translations/en.json"] = translations
	}
	if opts.HasLogo {
		files[LogoResourcePath] = logoPNG
	}

	for name, content := range files {
		if err := writeZipFile(zw, name, content); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}

func writeZipFile(zw *zip.Writer, name string, content []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := w.Write(content); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}
