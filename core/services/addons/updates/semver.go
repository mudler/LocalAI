// SPDX-License-Identifier: MIT
package updates

import (
	"regexp"
	"strconv"
	"strings"
)

// version is a parsed immutable version label: major.minor.patch with an
// optional pre-release, the form a catalog entry and a manifest use.
type version struct {
	major, minor, patch uint64
	pre                 []string
}

var versionText = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([a-z][a-z0-9.-]*))?$`)

// isVersion reports whether text is an immutable version label. A moving
// channel name is never one: the catalog refuses such a channel.
func isVersion(text string) bool { return versionText.MatchString(text) }

func parseVersion(text string) (version, bool) {
	m := versionText.FindStringSubmatch(text)
	if m == nil {
		return version{}, false
	}
	var v version
	for i, dst := range []*uint64{&v.major, &v.minor, &v.patch} {
		n, e := strconv.ParseUint(m[i+1], 10, 64)
		if e != nil {
			return version{}, false
		}
		*dst = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// compare orders two versions as semantic versioning does: a pre-release is
// lower than its release.
func (v version) compare(o version) int {
	for _, p := range [][2]uint64{{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, b := v.pre[i], o.pre[i]
		an, aerr := strconv.ParseUint(a, 10, 64)
		bn, berr := strconv.ParseUint(b, 10, 64)
		switch {
		case aerr == nil && berr == nil && an != bn:
			if an < bn {
				return -1
			}
			return 1
		case aerr == nil && berr != nil:
			return -1
		case aerr != nil && berr == nil:
			return 1
		case a != b:
			if a < b {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(v.pre) < len(o.pre):
		return -1
	case len(v.pre) > len(o.pre):
		return 1
	}
	return 0
}
