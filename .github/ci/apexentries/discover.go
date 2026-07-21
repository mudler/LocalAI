package main

import (
	"regexp"
	"strings"
)

// tierRE matches the tier marker APEX repos put at the end of a weight
// filename. Discovery is by suffix because the stem is not predictable from
// the repo name: six of the 45 repos drop a suffix ("-it", "-2603") or a
// vendor prefix ("NVIDIA-") that the repo name carries.
var tierRE = regexp.MustCompile(`-(I-)?(Quality|Balanced|Compact|Mini|Nano)\.gguf$`)

// Tier is one discovered build of an APEX repo.
type Tier struct {
	Label string
	File  GGUFFile
}

// DiscoverAPEXTiers splits a repo's weight files into the imatrix ladder and
// the plain ladder. mmproj files are never tiers.
func DiscoverAPEXTiers(files []GGUFFile) (imatrix, plain []Tier) {
	for _, f := range files {
		if strings.HasPrefix(f.Name, "mmproj") {
			continue
		}
		m := tierRE.FindStringSubmatch(f.Name)
		if m == nil {
			continue
		}
		if m[1] != "" {
			imatrix = append(imatrix, Tier{Label: "I-" + m[2], File: f})
			continue
		}
		plain = append(plain, Tier{Label: m[2], File: f})
	}
	return imatrix, plain
}

// DiscoverMMProj returns the repo's projector file, if it publishes one. The
// name varies across repos (mmproj.gguf, mmproj-F16.gguf,
// mmproj-step3.7-flash-f16.gguf), so match the prefix rather than a fixed name.
func DiscoverMMProj(files []GGUFFile) (GGUFFile, bool) {
	for _, f := range files {
		if strings.HasPrefix(f.Name, "mmproj") {
			return f, true
		}
	}
	return GGUFFile{}, false
}

// FileStem returns a tier's filename with its tier suffix removed.
func FileStem(t Tier) string {
	return tierRE.ReplaceAllString(t.File.Name, "")
}
