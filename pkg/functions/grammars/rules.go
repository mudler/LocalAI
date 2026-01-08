package grammars

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/utils"
)

type Rules map[string]string

func (rules Rules) ToGrammar(options ...func(*GrammarOption)) string {
	grammarOpts := &GrammarOption{}
	grammarOpts.Apply(options...)

	prefix := grammarOpts.Prefix
	maybeArray := grammarOpts.MaybeArray
	disableParallelNewLines := grammarOpts.DisableParallelNewLines
	maybeString := grammarOpts.MaybeString
	noMixedFreeString := grammarOpts.NoMixedFreeString

	var lines []string

	swapRoot := maybeArray || maybeString || prefix != ""

	// write down the computed rules.
	// if maybeArray is true, we need to add the array rule and slightly tweak the root rule
	for name, rule := range rules {
		if swapRoot && name == "root" {
			name = "realvalue"
		}
		lines = append(lines, fmt.Sprintf("%s ::= %s", name, rule))
	}

	if !swapRoot {
		return strings.Join(lines, "\n")
	}

	newRoot := "realvalue"
	if maybeArray {
		newRoot = "arr | realvalue"
	}

	freestringRule := "mixedstring"
	if noMixedFreeString {
		freestringRule = "freestring"
	}

	if prefix != "" {
		// quote newlines in suffix
		prefix = utils.EscapeNewLines(prefix)

		if maybeArray && maybeString {
			newRoot = "(" + newRoot + ")"
		}

		if maybeString {
			//newRoot = "( (\"" + suffix + "\" " + newRoot + ") | freestring ) "
			newRoot = "( \"" + prefix + "\" " + newRoot + " | " + freestringRule + " ) "
		} else {
			newRoot = "\"" + prefix + "\" " + "" + newRoot + ""
		}
	} else if maybeString {
		if maybeArray {
			//	newRoot = "(" + newRoot + ")"
		}

		newRoot = freestringRule + " | " + newRoot
	}

	lines = append(lines, fmt.Sprintf("%s ::= %s", "root", newRoot))
	if disableParallelNewLines {
		lines = append(lines, array)
	} else {
		lines = append(lines, arrayNewLines)
	}

	if maybeArray {
		if grammarOpts.ExpectStringsAfterJSON {
			lines = append(lines, `mixedstring ::= freestring | freestring arr freestring | (freestring realvalue freestring)* | realvalue | arr`)
		} else {
			lines = append(lines, `mixedstring ::= freestring | freestring arr | freestring realvalue | realvalue | arr`)
		}
	} else {
		if grammarOpts.ExpectStringsAfterJSON {
			lines = append(lines, `mixedstring ::= freestring | (freestring realvalue freestring)* | realvalue`)
		} else {
			lines = append(lines, `mixedstring ::= freestring | freestring realvalue | realvalue`)
		}
	}

	return strings.Join(lines, "\n")
}
