#!/usr/bin/env python3
"""Structured dataset from the plume review corpus.

One row per critique pass. Findings appear in three layouts across the corpus:
  A: "### 3. MAJOR — title"        (most reviews)
  B: "1. **MAJOR — title.**"       (01/02-review, the terse in-session passes)
  C: "### F1. MATERIAL — title"    (architecture-v1-review: an ACCURACY audit
                                    with its own taxonomy, not a design critique)
C is excluded from the design-critique dataset and reported separately, because
mixing taxonomies would be the kind of quiet category error this project keeps
catching in other people's specs.
"""
import re, glob, json, os

SEV = r'(BLOCKER|MAJOR|MINOR)'
rows, excluded = [], []

for path in sorted(glob.glob('docs/designs/reviews/*.md')):
    text = open(path).read()
    name = os.path.basename(path).replace('.md', '')

    if re.search(r'^###\s+F\d+\.\s+\*{0,2}(MATERIAL|MINOR)', text, re.M):
        n = len(re.findall(r'^###\s+F\d+\.', text, re.M))
        excluded.append((name, n, 'accuracy audit — MATERIAL/minor taxonomy'))
        continue

    f = re.findall(rf'^###\s+\d+\.\s+\*{{0,2}}{SEV}', text, re.M)          # layout A
    if not f:
        f = re.findall(rf'^\s*\d+\.\s+\*\*{SEV}\s*[—-]', text, re.M)       # layout B
    counts = {s: f.count(s) for s in ('BLOCKER', 'MAJOR', 'MINOR')}

    vm = re.search(r'\*\*Verdict\*\*:\s*\*{0,2}(PASS|REVISE)', text)
    # Independence is self-declared in every doc, as either a claim or a caveat.
    # Match the CAVEAT first: "Independence note: ... did not author" is a claim
    # of independence, while "Independence caveat: ... same session" is its
    # opposite, and a naive search for the word finds both.
    caveat = bool(re.search(r'Independence caveat|same session that authored', text))
    ind = (not caveat) and bool(re.search(r'\*\*Independence', text)) or 'recritique' in name

    rows.append({
        'review': name, 'design': name.split('-')[0],
        'pass': 'recritique' if 'recritique' in name else 'review',
        'independent': ind and not caveat,
        'shared_context_declared': caveat,
        'verdict': vm.group(1) if vm else None,
        **{k.lower(): v for k, v in counts.items()},
        'total': sum(counts.values()), 'lines': len(text.splitlines()),
    })

hdr = f"{'review':22} {'independent':12} {'verdict':8} {'B':>2} {'Ma':>3} {'Mi':>3} {'tot':>4} {'lines':>6}"
print(hdr); print('-' * len(hdr))
for r in rows:
    print(f"{r['review']:22} {str(r['independent']):12} {str(r['verdict']):8} "
          f"{r['blocker']:2} {r['major']:3} {r['minor']:3} {r['total']:4} {r['lines']:6}")
t = lambda k: sum(r[k] for r in rows)
print('-' * len(hdr))
print(f"{'TOTAL (n=%d passes)' % len(rows):22} {'':12} {'':8} "
      f"{t('blocker'):2} {t('major'):3} {t('minor'):3} {t('total'):4} {t('lines'):6}")

if excluded:
    print("\nExcluded from the design-critique dataset:")
    for n, c, why in excluded:
        print(f"  {n}: {c} findings — {why}")

zero = [r['review'] for r in rows if r['total'] == 0]
print(f"\nunparsed (zero findings): {zero if zero else 'none'}")
json.dump(rows, open(os.path.join(os.path.dirname(__file__), 'reviews.json'), 'w'), indent=1)
