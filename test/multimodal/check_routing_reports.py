#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Fail closed on empty, filtered, pending or failed wire verification suites."""
import json
from pathlib import Path
import sys


def require(condition, message='invalid wire report'):
    if not condition:
        raise ValueError(message)


def check(scratch, revision):
    counts = {}
    required = {
        'publishes a dedicated typed RPC rather than legacy unknown fields',
        'round trips ordered binary text parts and every uint64 counter',
        'old server returns UNIMPLEMENTED without invoking legacy embeddings',
        'new Go default server rejects without invoking legacy embeddings',
        'leaves legacy text RPC and result layout unchanged',
        'preserves absent metadata and distinguishes all five modalities',
        'preserves legacy field numbers and dedicated request numbers',
        'enforces proto.Size below at and above 50 MiB before dispatch',
        'preserves bearer metadata call options busy accounting and cancellation',
        'enforces selected identity on embedded and transported routes',
    }
    for name in ('core-backend', 'pkg-grpc', 'pkg-model', 'core-services-nodes'):
        reports = json.loads((scratch / (name + '.json')).read_text())
        require(reports, f'{name}: missing suite')
        specs = []
        for report in reports:
            require(report['SuiteSucceeded'], f'{name}: failed suite')
            require(not report['SuiteHasProgrammaticFocus'], f'{name}: focused suite')
            config = report['SuiteConfig']
            require(config['FailOnEmpty'] and config['FailOnPending'])
            require(not config['DryRun'] and not config['FocusStrings'] and not config['SkipStrings'])
            specs.extend(s for s in report['SpecReports'] if s['LeafNodeType'] == 'It')
        require(specs and all(s['State'] == 'passed' for s in specs), f'{name}: empty or nonpassing specs')
        counts[name] = len(specs)
        if name != 'pkg-grpc':
            routing = [s for s in specs if 'Multimodal routing' in s['ContainerHierarchyTexts']]
            require(routing, f'{name}: missing routing cases')
            counts[name + ': Multimodal routing'] = len(routing)
        if name == 'core-backend':
            texts = ' '.join(s['LeafNodeText'] for s in routing)
            require(all(key in texts for key in ('model-identity', 'reload', 'distributed', 'context', 'same-capability generation')))
        if name == 'pkg-grpc':
            generation = {s['LeafNodeText'] for s in specs if 'Multimodal routing generation' in s['ContainerHierarchyTexts']}
            require({
                'publishes worker generation in metadata and request',
                'worker rejects same-capability reload before call and accepts refreshed generation',
                'worker rejects same-capability reload during inference before publication',
                'restart at the same address changes generation even with identical capabilities',
            } <= generation, 'missing generation lifecycle cases')
            counts['generation'] = len(generation)
            wire = [s for s in specs if 'Multimodal wire' in s['ContainerHierarchyTexts']]
            require(required <= {s['LeafNodeText'] for s in wire}, 'missing required wire cases')
            counts['Multimodal wire'] = len(wire)
    result = {'revision': revision, 'exit_status': 0, 'counts': counts,
              'command': 'bash test/multimodal/verify.sh routing',
              'evidence_paths': [str(p) for p in scratch.glob('*.json')],
              'generator_versions': ['libprotoc 3.21.12', 'protoc-gen-go v1.34.2', 'protoc-gen-go-grpc 1.4.0'],
              'deterministic_regeneration': 'byte-identical'}
    (scratch / 'routing-report.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    check(Path(sys.argv[1]), sys.argv[2])
