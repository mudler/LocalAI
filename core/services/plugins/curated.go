package plugins

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

const curatedURLPrefix = "https://raw.githubusercontent.com/mudler/LocalAI/"
const curatedURLPath = "/plugins/.claude-plugin/marketplace.json"

// CuratedMarketplaceURL returns the URL of the curated marketplace that a
// build reads: the file at the v<semver> tag for a release build, and on
// master for any other build. The rule is the one of the official add-on
// library (spec 8.3).
func CuratedMarketplaceURL(version string) string {
	ref := "master"
	if catalog.IsReleaseVersion(version) {
		ref = version
	}
	return curatedURLPrefix + ref + curatedURLPath
}

// isCuratedURL reports whether u is the curated marketplace of some build.
func isCuratedURL(u string) bool {
	ref, ok := strings.CutPrefix(u, curatedURLPrefix)
	if !ok {
		return false
	}
	ref, ok = strings.CutSuffix(ref, curatedURLPath)
	return ok && ref != "" && !strings.Contains(ref, "/")
}
