// SPDX-License-Identifier: MIT
// Package the official index and its repository-local base configurations.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: gallery REPOSITORY {gallery|backend} OUTPUT")
		os.Exit(1)
	}
	if err := packageGallery(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func packageGallery(root, source, output string) error {
	if source != "gallery" && source != "backend" {
		return fmt.Errorf("unsupported gallery directory %q", source)
	}
	repository, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = repository.Close() }()
	body, err := repository.ReadFile(filepath.Join(source, "index.yaml"))
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return err
	}
	// The build operator explicitly selects the output directory via the CLI.
	if err := os.MkdirAll(output, 0700); err != nil { // #nosec G703 -- caller-selected output root
		return err
	}
	destination, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer func() { _ = destination.Close() }()
	// Keep the tree relative to the repository root so repeated base configs
	// share a layer, even when an index refers outside its own directory.
	const prefix = "github:mudler/LocalAI/"
	var walk func(*yaml.Node) error
	walk = func(n *yaml.Node) error {
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				value := n.Content[i+1]
				if n.Content[i].Value != "url" || value.Kind != yaml.ScalarNode || !strings.HasPrefix(value.Value, prefix) || !strings.HasSuffix(value.Value, "@master") {
					continue
				}
				path := strings.TrimSuffix(strings.TrimPrefix(value.Value, prefix), "@master")
				if !filepath.IsLocal(path) {
					return fmt.Errorf("base config escapes repository: %q", path)
				}
				config, err := repository.ReadFile(path)
				if err != nil {
					return err
				}
				if err := destination.MkdirAll(filepath.Dir(path), 0700); err != nil {
					return err
				}
				if err := destination.WriteFile(path, config, 0600); err != nil {
					return err
				}
				value.Value = filepath.ToSlash(path)
			}
		}
		for _, child := range n.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(&doc); err != nil {
		return err
	}
	body, err = yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return destination.WriteFile("index.yaml", body, 0600)
}
