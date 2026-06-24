// Command variant-proposals looks for gallery entries that are alternative
// builds of the same weights but are not grouped under one another, and writes
// a proposal for a human to accept or reject.
//
// It never decides. Grouping has gone wrong repeatedly in both directions, so
// the job's value is catching drift and surfacing candidates with their
// evidence, not automating the call. The scheduled workflow feeds its output to
// a pull request in the same shape as .github/checksum_checker.sh.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	index := flag.String("index", "gallery/index.yaml", "path to the gallery index")
	ledger := flag.String("ledger", "gallery/variant-exclusions.yaml", "path to the rejection ledger")
	bodyOut := flag.String("body-out", "", "write the pull request body here")
	apply := flag.Bool("apply", false, "write the proposed groupings back into the index")
	flag.Parse()

	if err := run(*index, *ledger, *bodyOut, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "variant-proposals:", err)
		os.Exit(1)
	}
}

func run(indexPath, ledgerPath, bodyOut string, apply bool) error {
	ix, err := LoadIndex(indexPath)
	if err != nil {
		return err
	}
	ledger, err := LoadLedger(ledgerPath)
	if err != nil {
		return err
	}

	result := Propose(ix, ledger)
	fmt.Print(RenderSummary(result))

	if !result.HasProposals() {
		// An empty pull request every night is how a proposal job gets muted.
		fmt.Println("nothing to propose")
		return nil
	}

	if bodyOut != "" {
		if err := os.WriteFile(bodyOut, []byte(RenderBody(result, ledgerPath)), 0o644); err != nil {
			return err
		}
	}

	if !apply {
		return nil
	}

	lines, err := ApplyFamilies(ix, result.Families)
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath, []byte(strings.Join(lines, "\n")), 0o644)
}
