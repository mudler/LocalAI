package piipattern

import "regexp"

// Compile validates src against the restricted grammar and, if it passes,
// compiles it to an RE2 program set to leftmost-longest matching so a hit grabs
// the whole secret (the entire key) rather than the shortest prefix.
func Compile(src string) (*regexp.Regexp, error) {
	if err := ValidatePattern(src); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(src)
	if err != nil {
		// ValidatePattern already parsed with the same flags, so this is
		// effectively unreachable, but surface it rather than panic.
		return nil, err
	}
	re.Longest()
	return re, nil
}
