#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check that the Ginkgo case inventories of suites.json match the specs in the code.

verify.sh runs each Ginkgo suite and compares the report with the cases listed in
scripts/addons/suites.json. This script makes the same comparison without running
the specs: it runs every Ginkgo command of the file with -ginkgo.dry-run, which
builds the test binary and reports the specs it would run. A spec that is in the
code but not in the inventory, or in the inventory but not in the code, is
printed by name and the exit status is 1.

  python3 scripts/addons/inventory.py [suite ...]           check (default: all)
  python3 scripts/addons/inventory.py --write [suite ...]   update suites.json

--write drops the stale cases and appends the missing ones. Review the diff.
"""
import argparse
import collections
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
suites_file = root / 'scripts/addons/suites.json'
REPORT = '-ginkgo.json-report='


def dry_run(command, scratch):
    """Return (report path of the command, cases a real run would report)."""
    declared = next(a for a in command if a.startswith(REPORT))[len(REPORT):]
    out = pathlib.Path(scratch) / re.sub(r'\W', '_', declared)
    argv = [a for a in command if not a.startswith(REPORT)] + [REPORT + str(out), '-ginkgo.dry-run']
    focus = next((a.split('=', 1)[1] for a in command if a.startswith('-ginkgo.focus=')), None)
    result = subprocess.run(argv, cwd=root, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    # A dry run still runs the plain Go tests of the package, and they may fail
    # for reasons unrelated to the inventory. Only a missing report is an error.
    if not out.exists():
        sys.exit('dry run failed: %s\n%s' % (' '.join(command), result.stdout[-2000:]))
    cases = []
    for report in json.loads(out.read_text()):
        for spec in report['SpecReports']:
            if spec['LeafNodeType'] != 'It' or spec['State'] == 'skipped':
                continue
            text = ' '.join((spec.get('ContainerHierarchyTexts') or []) + [spec['LeafNodeText']])
            if focus and not re.search(focus, text):
                continue
            cases.append(spec['LeafNodeText'])
    return declared, cases


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--write', action='store_true', help='update suites.json instead of failing')
    parser.add_argument('suites', nargs='*')
    args = parser.parse_args()

    inventory = json.loads(suites_file.read_text())
    names = args.suites or [n for n, e in inventory.items() if e['kind'] == 'ginkgo']
    for name in names:
        if name not in inventory or inventory[name]['kind'] != 'ginkgo':
            sys.exit('not a Ginkgo suite of suites.json: ' + name)

    drift = []
    with tempfile.TemporaryDirectory(dir=os.environ.get('TMPDIR')) as scratch:
        for name in names:
            entry = inventory[name]
            reports = entry['reports'] if 'reports' in entry else [dict(path=entry['report'], cases=entry['cases'])]
            found = collections.defaultdict(list)
            for command in entry['commands']:
                declared, cases = dry_run(command, scratch)
                found[os.path.basename(declared)].extend(cases)
            for report in reports:
                actual = found.pop(os.path.basename(report['path']), None)
                if actual is None:
                    sys.exit('%s: no command writes %s' % (name, report['path']))
                listed = collections.Counter(report['cases'])
                missing = list((collections.Counter(actual) - listed).elements())
                stale = list((listed - collections.Counter(actual)).elements())
                if not missing and not stale:
                    continue
                drift.append((name, report['path'], missing, stale))
                if args.write:
                    keep = collections.Counter(actual)
                    kept = []
                    for case in report['cases']:
                        if keep[case] > 0:
                            keep[case] -= 1
                            kept.append(case)
                    report['cases'] = kept + missing
            if args.write and 'reports' not in entry:
                entry['cases'] = reports[0]['cases']
            if found:
                sys.exit('%s: reports not listed in the inventory: %s' % (name, ', '.join(found)))

    for name, path, missing, stale in drift:
        print('suite %s, report %s' % (name, path))
        for case in missing:
            print('  missing from suites.json: ' + case)
        for case in stale:
            print('  listed in suites.json but not in the code: ' + case)
    if not drift:
        print('Ginkgo inventories match the specs of %d suites.' % len(names))
        return 0
    if args.write:
        suites_file.write_text(json.dumps(inventory, indent=2) + '\n')
        print('suites.json updated. Review the diff.')
        return 0
    print('\nThe Ginkgo inventory in scripts/addons/suites.json is out of date.\n'
          'Run `make addons-inventory-write` (or `python3 scripts/addons/inventory.py --write`)\n'
          'and commit the change to scripts/addons/suites.json.')
    return 1


if __name__ == '__main__':
    sys.exit(main())
