#!/usr/bin/env python3
"""Check the curated catalog against Google's public model-number list.

This is a maintainer check, not a request made during account creation.
It reads public device specifications only, never application configuration.
"""

import argparse
import csv
import hashlib
import io
import json
from pathlib import Path
import sys
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--csv', type=Path, help='Use a previously downloaded official CSV')
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    catalog = json.loads((root / 'internal/deviceprofile/catalog.json').read_text(encoding='utf-8'))
    source = catalog['sources']['google-play']['url']
    if args.csv:
        content = args.csv.read_bytes()
    else:
        request = urllib.request.Request(source, headers={'User-Agent': 'DeviceCatalogVerification/1.0'})
        with urllib.request.urlopen(request, timeout=60) as response:
            content = response.read(32 * 1024 * 1024 + 1)
        if len(content) > 32 * 1024 * 1024:
            raise ValueError('Public device CSV exceeds the size limit')
    encoding = 'utf-16' if content.startswith((b'\xff\xfe', b'\xfe\xff')) else 'utf-8-sig'
    rows = list(csv.DictReader(io.StringIO(content.decode(encoding))))
    by_model = {}
    for row in rows:
        by_model.setdefault(row['Model'], []).append(row)
    results = []
    for profile in catalog['devices']:
        brand, code = profile['model_name'].split('|', 1)
        matches = [row for row in by_model.get(code, [])
                   if row['Marketing Name'].strip().casefold() == profile['certified_name'].casefold()
                   and row['Retail Branding'].strip().casefold() == brand.casefold()]
        results.append({'id': profile['id'], 'model': code, 'matched': bool(matches)})
    report = {
        'source': source,
        'source_sha256': hashlib.sha256(content).hexdigest(),
        'verified_at': catalog['verified_at'],
        'catalog_models': len(results),
        'matched_models': sum(row['matched'] for row in results),
        'results': results,
    }
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if all(row['matched'] for row in results) else 1


if __name__ == '__main__':
    sys.exit(main())
