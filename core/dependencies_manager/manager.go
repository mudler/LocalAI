package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v3"
)

type Asset struct {
	FileName string `yaml:"filename"`
	URL      string `yaml:"url"`
	SHA      string `yaml:"sha"`
}

func main() {

	// read the YAML file which contains a list of assets
	// and download them in the asset path
	assets := []Asset{}

	assetFile := os.Args[1]
	destPath := os.Args[2]

	// read the YAML file
	f, err := os.ReadFile(assetFile)
	if err != nil {
		panic(err)
	}
	// unmarshal the YAML data into a struct
	if err := yaml.Unmarshal(f, &assets); err != nil {
		panic(err)
	}

	// download the assets
	for _, asset := range assets {
		uri := downloader.URI(asset.URL)
		if err := uri.DownloadFile(filepath.Join(destPath, asset.FileName), asset.SHA, 1, 1, utils.DisplayDownloadFunction); err != nil {
			panic(err)
		}
	}

	fmt.Println("Finished downloading assets")
}
