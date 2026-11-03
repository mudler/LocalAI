// SPDX-License-Identifier: MIT
package contract

import "strings"

// CompareVersions orders two add-on versions by semantic versioning: it
// returns -1, 0 or 1. A pre-release sorts below its release. Build metadata
// does not occur in an add-on version.
func CompareVersions(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa.num[i] != pb.num[i] {
			if pa.num[i] < pb.num[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.pre == "" && pb.pre == "":
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	as, bs := strings.Split(pa.pre, "."), strings.Split(pb.pre, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aok := atoiBounded(as[i])
		bn, bok := atoiBounded(bs[i])
		switch {
		case aok && bok:
			if an < bn {
				return -1
			}
			return 1
		case aok:
			return -1
		case bok:
			return 1
		case as[i] < bs[i]:
			return -1
		default:
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

type parsedVersion struct {
	num [3]int
	pre string
}

func splitVersion(v string) parsedVersion {
	core, pre, _ := strings.Cut(v, "-")
	var s parsedVersion
	for i, p := range strings.SplitN(core, ".", 3) {
		n, _ := atoiBounded(p)
		s.num[i] = n
	}
	s.pre = pre
	return s
}

func atoiBounded(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' || n > 1<<40 {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
