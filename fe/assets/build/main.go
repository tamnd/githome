// Command build regenerates the embedded web-front bundle in fe/assets/dist.
// It uses esbuild to bundle and minify the CSS and JS entry points, then
// content-hashes each output and writes a manifest.json the server reads at
// boot. Run after editing anything under fe/assets/src.
//
//	go run ./fe/assets/build
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
}

func run() error {
	root := findModuleRoot()
	srcDir := filepath.Join(root, "fe", "assets", "src")
	distDir := filepath.Join(root, "fe", "assets", "dist")

	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}

	// Remove stale dist files (keep manifest.json until we rewrite it).
	entries, _ := os.ReadDir(distDir)
	for _, e := range entries {
		if e.Name() == "manifest.json" {
			continue
		}
		_ = os.Remove(filepath.Join(distDir, e.Name()))
	}

	manifest := map[string]string{}

	// Bundle CSS.
	cssResult := api.Build(api.BuildOptions{
		EntryPoints:      []string{filepath.Join(srcDir, "css", "app.css")},
		Bundle:           true,
		MinifyWhitespace: true,
		MinifySyntax:     true,
		Outdir:           distDir,
		Write:            false,
	})
	if len(cssResult.Errors) > 0 {
		for _, e := range cssResult.Errors {
			fmt.Fprintln(os.Stderr, "css:", e.Text)
		}
		return fmt.Errorf("%d CSS build error(s)", len(cssResult.Errors))
	}
	for _, file := range cssResult.OutputFiles {
		h := contentHash(file.Contents)
		name := "app." + h + ".css"
		if err := os.WriteFile(filepath.Join(distDir, name), file.Contents, 0o644); err != nil {
			return err
		}
		manifest["app.css"] = name
		fmt.Printf("css: %s (%d bytes)\n", name, len(file.Contents))
	}

	// Bundle JS — only if a TS entry point exists.
	tsEntry := filepath.Join(srcDir, "ts", "app.ts")
	if _, err := os.Stat(tsEntry); err == nil {
		jsResult := api.Build(api.BuildOptions{
			EntryPoints:       []string{tsEntry},
			Bundle:            true,
			MinifyWhitespace:  true,
			MinifySyntax:      true,
			MinifyIdentifiers: true,
			Target:            api.ES2019,
			Outdir:            distDir,
			Write:             false,
		})
		if len(jsResult.Errors) > 0 {
			for _, e := range jsResult.Errors {
				fmt.Fprintln(os.Stderr, "js:", e.Text)
			}
			return fmt.Errorf("%d JS build error(s)", len(jsResult.Errors))
		}
		for _, file := range jsResult.OutputFiles {
			h := contentHash(file.Contents)
			name := "app." + h + ".js"
			if err := os.WriteFile(filepath.Join(distDir, name), file.Contents, 0o644); err != nil {
				return err
			}
			manifest["app.js"] = name
			fmt.Printf("js: %s (%d bytes)\n", name, len(file.Contents))
		}
	} else {
		// No JS source: keep the existing JS file if present, otherwise skip.
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".js" {
				manifest["app.js"] = e.Name()
				// Re-write it since we deleted it above.
				data, err := os.ReadFile(filepath.Join(distDir, e.Name()))
				if err == nil {
					_ = os.WriteFile(filepath.Join(distDir, e.Name()), data, 0o644)
				}
				break
			}
		}
	}

	// Write manifest.
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(distDir, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println("manifest:", filepath.Join(distDir, "manifest.json"))
	return nil
}

func contentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:16]
}

func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
