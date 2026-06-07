// Command build compiles the Githome web front assets. It generates the theme
// CSS from the Go palettes, bundles and minifies the CSS and TypeScript entries
// with the esbuild Go API (no Node at build or run time), content-hashes each
// output, and writes the hashed files plus manifest.json into the dist tree that
// fe/assets embeds. esbuild bundles and minifies CSS as well as JS, so the build
// needs no separate CSS toolchain. See implementation/01 and implementation/04.
//
// Run it with `make assets`. `make assets-check` re-runs it and fails if the
// committed dist tree or the generated CSS drifts, which keeps the embedded
// assets reproducible from source.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("asset build: %v", err)
	}
}

func run() error {
	srcDir := flag.String("src", filepath.Join("fe", "assets", "src"), "asset source directory")
	distDir := flag.String("dist", filepath.Join("fe", "assets", "dist"), "asset output directory")
	flag.Parse()

	// 1. Generate the theme CSS from the palettes so app.css can import it.
	themesCSS, err := generateThemesCSS()
	if err != nil {
		return fmt.Errorf("generate themes: %w", err)
	}
	themesPath := filepath.Join(*srcDir, "css", "themes.gen.css")
	if err := writeIfChanged(themesPath, []byte(themesCSS)); err != nil {
		return err
	}

	// 2. Bundle and minify the CSS and JS entries.
	cssOut, err := bundle(filepath.Join(*srcDir, "css", "app.css"), api.LoaderCSS)
	if err != nil {
		return fmt.Errorf("bundle css: %w", err)
	}
	jsOut, err := bundle(filepath.Join(*srcDir, "ts", "app.ts"), api.LoaderTS)
	if err != nil {
		return fmt.Errorf("bundle js: %w", err)
	}

	// 3. Content-hash and write the dist tree plus the manifest.
	if err := os.MkdirAll(*distDir, 0o755); err != nil {
		return err
	}
	if err := cleanDist(*distDir); err != nil {
		return err
	}
	manifest := map[string]string{}
	cssName, err := writeHashed(*distDir, "app", "css", cssOut)
	if err != nil {
		return err
	}
	manifest["app.css"] = cssName
	jsName, err := writeHashed(*distDir, "app", "js", jsOut)
	if err != nil {
		return err
	}
	manifest["app.js"] = jsName

	if err := writeManifest(*distDir, manifest); err != nil {
		return err
	}
	log.Printf("assets built: %s, %s", cssName, jsName)
	return nil
}

// bundle runs esbuild over one entry with bundling and minification on, and
// returns the single output's bytes. A bundling error (an unresolved import, a
// syntax error) is returned, never swallowed.
func bundle(entry string, loader api.Loader) ([]byte, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{entry},
		Bundle:            true,
		Write:             false,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Target:            api.ES2019,
		Loader:            map[string]api.Loader{filepath.Ext(entry): loader},
		Charset:           api.CharsetUTF8,
		LogLevel:          api.LogLevelSilent,
	})
	if len(result.Errors) > 0 {
		msgs := api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage})
		return nil, fmt.Errorf("%s", strings.Join(msgs, "\n"))
	}
	if len(result.OutputFiles) != 1 {
		return nil, fmt.Errorf("expected 1 output for %s, got %d", entry, len(result.OutputFiles))
	}
	return result.OutputFiles[0].Contents, nil
}

// writeHashed writes content to dist/<base>.<hash>.<ext> and returns the file
// name. The hash is the first 16 hex chars of the sha256, enough to be collision
// safe for an asset set while keeping URLs short.
func writeHashed(distDir, base, ext string, content []byte) (string, error) {
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])[:16]
	name := fmt.Sprintf("%s.%s.%s", base, hash, ext)
	if err := os.WriteFile(filepath.Join(distDir, name), content, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// writeManifest writes manifest.json with stable key ordering so the committed
// file does not churn between builds.
func writeManifest(distDir string, manifest map[string]string) error {
	keys := make([]string, 0, len(manifest))
	for k := range manifest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(manifest))
	for _, k := range keys {
		ordered[k] = manifest[k]
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(distDir, "manifest.json"), data, 0o644)
}

// cleanDist removes the previously hashed outputs so a rebuild does not leave a
// stale app.<oldhash>.css behind. It keeps the directory and any non-asset files.
func cleanDist(distDir string) error {
	entries, err := os.ReadDir(distDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".js") || name == "manifest.json" {
			if err := os.Remove(filepath.Join(distDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeIfChanged writes content only when it differs from what is on disk, so a
// no-op build does not rewrite a file's mtime and confuse make.
func writeIfChanged(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
