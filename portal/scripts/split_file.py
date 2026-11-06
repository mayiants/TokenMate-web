#!/usr/bin/env python3
"""Split a Go file by function groups. Usage:
python3 scripts/split_file.py internal/routes/admin.go admin_users:145-286 admin_providers:418-560 ...
"""

import sys, os, re

filepath = sys.argv[1]
groups = sys.argv[2:]  # format: "output_name:start-end" or "output_name:start"

with open(filepath) as f:
    lines = f.readlines()

# Extract package + imports (everything before first func/type)
header_end = 0
for i, line in enumerate(lines):
    if re.match(r'^(func |type \w+ struct)', line.strip()):
        header_end = i
        break

header = ''.join(lines[:header_end])
total = len(lines)

for group in groups:
    parts = group.split(':')
    fname = parts[0]
    ranges = parts[1]

    # Parse ranges like "145-286,329-417"
    line_nums = set()
    for r in ranges.split(','):
        if '-' in r:
            start, end = r.split('-')
            for n in range(int(start), int(end) + 1):
                line_nums.add(n)
        else:
            line_nums.add(int(r))

    content = header + '\n'
    for i in sorted(line_nums):
        if 0 < i <= total:
            content += lines[i - 1]  # 1-indexed to 0-indexed

    outpath = os.path.join(os.path.dirname(filepath), fname + '.go')
    with open(outpath, 'w') as f:
        f.write(content)
    print(f'{outpath}: {len(line_nums)} lines')

# Now trim original to only keep core functions (not extracted)
# Find what was extracted
extracted_lines = set()
for group in groups:
    parts = group.split(':')
    ranges = parts[1]
    for r in ranges.split(','):
        if '-' in r:
            start, end = r.split('-')
            for n in range(int(start), int(end) + 1):
                extracted_lines.add(n)
        else:
            extracted_lines.add(int(r))

# Write remaining to original file
remaining = header + '\n'
for i in range(header_end + 1, total + 1):
    if i not in extracted_lines:
        remaining += lines[i - 1]

with open(filepath, 'w') as f:
    f.write(remaining)
print(f'{filepath}: {total - len(extracted_lines)} lines remaining')
