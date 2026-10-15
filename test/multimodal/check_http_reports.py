#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Fail closed on empty, filtered, pending or failed HTTP verification suites.

Each package has its own Ginkgo report. The endpoint suite must execute both
named groups with every required case exactly once, including one case for
every error status the contract lists.
"""
import json
from pathlib import Path
import sys

HTTP_GROUP = 'Multimodal HTTP'
LEGACY_GROUP = 'Legacy embedding response'
PACKAGES = ('core-http-endpoints-openai', 'core-http-middleware', 'core-http-auth', 'core-backend',
            'core-services-routing-piiadapter')
# Floor on passed specs of the PII adapter suite: the real total at the time of
# the pin. Raise it when specs are added; never lower it.
PIIADAPTER_MIN_PASSED = 22

STATUS_CASES = {
    '400 backend invalid argument',
    '400 empty content array',
    '400 invalid structure rejects unknown content fields',
    '400 structured input conflicts with messages',
    '413 acquisition limit',
    '415 backend unsupported media format',
    '429 backend busy',
    '502 backend cancellation while the client is alive',
    '413 backend resource exhausted',
    '415 unsupported media format',
    '422 backend refuses after probing',
    '422 model cannot consume the modality',
    '429 preparation capacity exhausted',
    '501 old backend metadata',
    '501 unimplemented multimodal RPC',
    '502 corrupt backend result',
    '502 fetch failure',
    '502 inference transport failure',
    '504 fetch timeout',
    '504 inference deadline',
}
REQUIRED_STATUSES = {400, 413, 415, 422, 429, 501, 502, 504}

ENDPOINT_CASES = {
    'blocks a plain string and the same text in a content object alike',
    'sends redacted string and text-part content to the backend',
    'sends redacted text to the legacy RPC for a plain string input',
    'returns admission and loading errors unchanged so the application handler maps them',
    'treats missing consumption on an item with media as a corrupt result',
    'accepts missing consumption on a text-only item',
    'charges Go-known media bytes even when the backend under-reports',
    'checks selected loaded-model metadata before fetching any media',
    'denies unauthenticated requests before any metadata read or fetch',
    'encodes base64 float32 little endian when requested',
    'holds the preparation slot during inference and releases it afterwards',
    'keeps request indexes for a reordered mixed batch',
    'keeps structured text, URLs and queries out of every log line',
    'maps an exceeded request deadline during inference to 504',
    'passes every part shape in order without implicit template or prefix',
    "pools per-token results exactly once with the model's scheme",
    'propagates client cancellation to a stalled fetch',
    'propagates client cancellation to inference and returns no vectors',
    'rejects a NaN value with 502 and no envelope',
    'rejects a dimension mismatch with 502 and no envelope',
    'rejects a empty vector with 502 and no envelope',
    'rejects a final with 2 tokens with 502 and no envelope',
    'rejects a infinite value with 502 and no envelope',
    'rejects a nil result with 502 and no envelope',
    'rejects a per-token shape wrong with 502 and no envelope',
    'rejects a zero vector with 502 and no envelope',
    'rejects aggregate media over the request limit across items with 413',
    'rejects an undeclared layout when Go-side pooling needs per-token vectors, as the legacy path does',
    'rejects consumption wrapping past the maximum without unsigned wraparound',
    'releases the slot when inference fails',
    'returns no partial envelope when a later item fails',
    'serves both documented aliases and the engine alias with one vector per item',
    'supplies remaining budgets per item and deducts actual consumption between items',
    'treats consumption above the supplied remaining budget as a corrupt result',
    'validates proto.Size below the transport ceiling before dispatch',
}

LEGACY_CASES = {
    'accepts the dimensions field without changing the returned vector',
    'embeds a string input through the legacy RPC unchanged',
    'embeds string arrays one vector per element with request indexes',
    'embeds token arrays through EmbeddingTokens',
    'keeps legacy backend errors flowing as before',
    'keeps rejecting input together with messages',
    'renders messages into one legacy input',
    'returns base64 little endian floats when requested',
}

BACKEND_CASES = {
    'returns a final vector unchanged',
    'accepts a final vector whose shape was not reported',
    'pools per-token results with the configured scheme exactly once',
    'classifies pooling and layout incompatibility as a pooling compatibility error',
    'rejects pooled zero and nonfinite vectors',
    'checks modalities against metadata the way the session does',
    'nil response',
    'missing result',
    'empty vector',
    'NaN',
    'negative infinity',
    'zero vector',
    'declared dimension disagrees',
    'final layout with several vectors',
    'per-token matrix of the wrong size',
}


def require(condition, message='invalid http report'):
    if not condition:
        raise ValueError(message)


def load_specs(path, name):
    reports = json.loads(Path(path).read_text())
    require(reports, name + ': missing suite')
    specs = []
    for report in reports:
        require(report['SuiteSucceeded'], name + ': failed suite')
        require(not report['SuiteHasProgrammaticFocus'], name + ': focused suite')
        config = report['SuiteConfig']
        require(config['FailOnEmpty'] and config['FailOnPending'], name + ': pending or empty suites allowed')
        require(not config['DryRun'] and not config['FocusStrings'] and not config['SkipStrings'],
                name + ': filtered run')
        specs.extend(s for s in report['SpecReports'] if s['LeafNodeType'] == 'It')
    require(specs, name + ': empty suite')
    # A failed spec already fails its suite. Specs of this feature may never be
    # skipped or pending; unrelated pre-existing specs of a shared package may
    # carry their own, unchanged, skip.
    for spec in specs:
        ours = {HTTP_GROUP, LEGACY_GROUP} & set(spec['ContainerHierarchyTexts'])
        if ours or spec['State'] != 'skipped':
            require(spec['State'] == 'passed', name + ': not passed: ' + str(spec.get('LeafNodeText')))
    require(any(s['State'] == 'passed' for s in specs), name + ': nothing executed')
    return specs


def grouped(specs, group):
    return [s for s in specs if group in s['ContainerHierarchyTexts']]


def require_cases(specs, group, required, where):
    members = grouped(specs, group)
    for name in sorted(required):
        matched = [s for s in members if s['LeafNodeText'] == name]
        require(len(matched) == 1, where + ': case missing or repeated: ' + name)
    return members


def check(scratch, revision, command='bash test/multimodal/verify.sh http'):
    counts = {}
    for name in PACKAGES:
        specs = load_specs(Path(scratch) / (name + '.json'), name)
        counts[name] = sum(s['State'] == 'passed' for s in specs)
        counts[name + ': unrelated skipped'] = sum(s['State'] == 'skipped' for s in specs)
        if name == 'core-http-endpoints-openai':
            http = require_cases(specs, HTTP_GROUP, ENDPOINT_CASES | STATUS_CASES, name)
            legacy = require_cases(specs, LEGACY_GROUP, LEGACY_CASES, name)
            statuses = {int(case.split()[0]) for case in STATUS_CASES}
            require(statuses == REQUIRED_STATUSES, name + ': status cases do not cover the contract')
            counts[HTTP_GROUP] = len(http)
            counts[LEGACY_GROUP] = len(legacy)
        if name == 'core-backend':
            result = require_cases(specs, HTTP_GROUP, BACKEND_CASES, name)
            counts[name + ': ' + HTTP_GROUP] = len(result)
        if name == 'core-services-routing-piiadapter':
            require(counts[name] >= PIIADAPTER_MIN_PASSED, name + ': fewer passed specs than the pinned total')
    out = {'revision': revision, 'exit_status': 0, 'command': command, 'counts': counts,
           'status_cases': sorted(STATUS_CASES),
           'evidence_paths': [str(Path(scratch) / (n + '.json')) for n in PACKAGES]}
    Path(scratch, 'http-report.json').write_text(json.dumps(out, indent=2) + '\n')
    return out


if __name__ == '__main__':
    print(json.dumps(check(Path(sys.argv[1]), sys.argv[2], *sys.argv[3:4]), indent=2))
