#!/usr/bin/env python3
"""Split routes/admin.go into smaller files by domain."""

import re

with open('internal/routes/admin.go') as f:
    text = f.read()

# Extract package declaration + imports (shared header)
header_end = text.index('type exportLimitEntry')
header = text[:header_end]

# Get all function/type start positions
func_starts = []
for m in re.finditer(r'^(func \(r \*AdminRoutes\) |func |type \w+ struct)', text, re.MULTILINE):
    func_starts.append((m.start(), m.group()))

# Define which file each function goes to
mapping = {
    'admin.go': [
        'exportLimitEntry', 'checkExportLimit', 'AdminRoutes', 'NewAdminRoutes', 'Register',
    ],
    'admin_users.go': [
        'listUsers', 'updateUser', 'deleteUser',
    ],
    'admin_dashboard.go': [
        'getAnalytics', 'getAdminMode',
    ],
    'admin_providers.go': [
        'listProviders', 'createProvider', 'updateProvider', 'toggleProvider', 'deleteProvider',
    ],
    'admin_models.go': [
        'listServiceModels', 'updateServiceModel', 'batchValidateModels',
        'listModelsGrouped', 'listModelsAdmin', 'getModelsStatus', 'getSyncStatus',
    ],
    'admin_keypool.go': [
        'getKeyPool', 'getKeyHistory', 'addKeyPoolKey', 'updateKeyPoolKey',
        'validateKeyAsync', 'deleteKeyPoolKey', 'deleteKeyPoolKeySimple',
        'toggleKeyPoolKey', 'toggleKeyPoolKeySimple', 'importKeyPoolKeys', 'exportKeyPoolKeys',
    ],
    'admin_openrouter.go': [
        'syncOpenRouter', 'syncOpenRouterLive', 'validateOpenRouter', 'getOpenRouterModels',
        'syncFromAIModelsStub',
    ],
}

# Build reverse mapping: function_name -> file_name
func_to_file = {}
for fname, funcs in mapping.items():
    for fn in funcs:
        func_to_file[fn] = fname

# Find function boundaries
import sys
functions = []
for i, (pos, decl) in enumerate(func_starts):
    # Extract the function name
    name_match = re.match(r'(?:func \(.*?\) )?(\w+)', decl)
    if not name_match:
        continue
    name = name_match.group(1)

    # Find the matching closing brace
    # Start from the opening brace
    brace_pos = text.find('{', pos)
    if brace_pos < 0:
        continue
    depth = 1
    j = brace_pos + 1
    while j < len(text) and depth > 0:
        if text[j] == '{':
            depth += 1
        elif text[j] == '}':
            depth -= 1
        j += 1
    end = j

    functions.append((name, pos, end))

# Write each file
files = {}
for fname in set(func_to_file.values()):
    files[fname] = [header]

for name, start, end in functions:
    fname = func_to_file.get(name)
    if not fname:
        print(f'WARNING: unmapped function: {name}', file=sys.stderr)
        fname = 'admin.go'  # default fallback
    files[fname].append(text[start:end])

# Write output files
for fname, chunks in sorted(files.items()):
    content = '\n\n'.join(chunks).strip() + '\n'
    path = f'internal/routes/{fname}'
    with open(path, 'w') as f:
        f.write(content)
    lines = content.count('\n')
    print(f'{path}: {lines} lines')

print(f'\nOriginal admin.go: {text.count(chr(10))} lines')
print(f'Split into {len(files)} files')
