// Package adapter injects gateway credentials into AI clients. All client
// specifics come from the portal's adapter manifest, so supporting a new
// client or a changed config format is a data change (portal DB), not an
// agent release. The workbuddy manifest format was pinned by the P0
// verification on a real Linux install (see
// agent/docs/workbuddy-p0-verification-20261106.md); the Windows path is the
// same relative layout per package code inspection.
package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mayiants/ai-gateway-go/agent/internal/api"
)

// Manifest is the adapter-specific detail blob inside api.AdapterManifest.
type Manifest struct {
	Status         string                   `json:"status"`                    // pending_p0_verification | active
	InstallPaths   []string                 `json:"install_paths,omitempty"`   // candidate client install markers (exe paths)
	ConfigFormat   string                   `json:"config_format,omitempty"`   // json (more formats later)
	Fields         map[string]string        `json:"fields,omitempty"`          // dotted JSON path -> "base_url" | "api_key"
	Entries        []map[string]interface{} `json:"entries,omitempty"`         // template objects upserted into an array (workbuddy models.json)
	EntryArrayPath string                   `json:"entry_array_path,omitempty"` // dotted path to the target array, default "models"
	EntryKeyField  string                   `json:"entry_key_field,omitempty"`  // identity field for the upsert, default "id"
	VersionArgs    []string                 `json:"version_args,omitempty"`    // args to probe client version, e.g. ["--version"]
	VersionHint    string                   `json:"version_hint,omitempty"`    // substring expected in version output
	EnvExpandNote  string                   `json:"env_expand_note,omitempty"`
}

// Pending reports whether the manifest explicitly says injection is not yet
// verified. The agent stores the key and defers all config writes.
func Pending(m *api.AdapterManifest) bool {
	var detail Manifest
	_ = json.Unmarshal(m.Manifest, &detail)
	return detail.Status == "pending_p0_verification" || m.ConfigPath == "" && detail.Status != "active"
}

// Detect reports whether the client appears installed and where.
func Detect(m *api.AdapterManifest) (installed bool, version string, err error) {
	var detail Manifest
	if err := json.Unmarshal(m.Manifest, &detail); err != nil {
		return false, "", fmt.Errorf("adapter manifest unreadable: %w", err)
	}
	for _, p := range detail.InstallPaths {
		full := ExpandEnv(p)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			return true, probeVersion(full, detail), nil
		}
	}
	return false, "", nil
}

func probeVersion(exe string, detail Manifest) string {
	args := detail.VersionArgs
	if len(args) == 0 {
		args = []string{"--version"}
	}
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// ExpandEnv replaces Windows-style %VAR% (and $VAR for dev builds) in
// manifest paths with environment values.
func ExpandEnv(s string) string {
	if runtime.GOOS == "windows" {
		s = os.Expand(s, func(k string) string { return os.Getenv(k) })
	}
	return os.ExpandEnv(s)
}

// Apply injects baseURL + apiKey into the client per the manifest. It backs
// up an existing config once (the backup path is recorded in state), writes
// atomically, and verifies the values round-trip.
func Apply(m *api.AdapterManifest, baseURL, apiKey string) (backupPath string, applied bool, err error) {
	if Pending(m) {
		return "", false, fmt.Errorf("adapter manifest is pending P0 verification — injection skipped, key stored only")
	}
	var detail Manifest
	if err := json.Unmarshal(m.Manifest, &detail); err != nil {
		return "", false, fmt.Errorf("adapter manifest unreadable: %w", err)
	}

	switch m.InjectMethod {
	case "env":
		return "", false, fmt.Errorf("env injection not implemented yet — extend after P0 pins the variable names")
	case "file", "":
	default:
		return "", false, fmt.Errorf("unknown inject_method %q", m.InjectMethod)
	}
	if detail.ConfigFormat != "json" {
		return "", false, fmt.Errorf("unsupported config_format %q", detail.ConfigFormat)
	}

	cfgPath := ExpandEnv(m.ConfigPath)
	raw, readErr := os.ReadFile(cfgPath)
	exists := readErr == nil

	var doc map[string]interface{}
	if exists {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", false, fmt.Errorf("existing config %s is not valid JSON: %w", cfgPath, err)
		}
	} else {
		dir := filepath.Dir(cfgPath)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return "", false, fmt.Errorf("config directory %s missing — is the client installed?", dir)
		}
		doc = map[string]interface{}{}
	}

	for dotted, kind := range detail.Fields {
		var value string
		switch kind {
		case "base_url":
			value = baseURL
		case "api_key":
			value = apiKey
		default:
			return "", false, fmt.Errorf("manifest field %s has unknown kind %q", dotted, kind)
		}
		setPath(doc, strings.Split(dotted, "."), value)
	}
	if len(detail.Entries) > 0 {
		if err := upsertEntries(doc, &detail, baseURL, apiKey); err != nil {
			return "", false, err
		}
	}
	if len(detail.Fields) == 0 && len(detail.Entries) == 0 {
		return "", false, fmt.Errorf("adapter manifest has neither fields nor entries — nothing to inject")
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", false, err
	}

	if exists {
		backupPath = cfgPath + ".woodgit-backup"
		bkRaw, bkErr := os.ReadFile(backupPath)
		if bkErr != nil || !bytes.Equal(bkRaw, raw) {
			// No backup yet, or the config changed since our backup: refresh it.
			stamped := fmt.Sprintf("%s.woodgit-backup-%s", cfgPath, time.Now().Format("20060102150405"))
			if err := os.WriteFile(stamped, raw, 0o600); err != nil {
				return "", false, fmt.Errorf("backup config: %w", err)
			}
			_ = os.WriteFile(backupPath, raw, 0o600)
		}
	}

	tmp := cfgPath + ".woodgit-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		os.Remove(tmp)
		return "", false, fmt.Errorf("replace config: %w", err)
	}
	return backupPath, true, nil
}

// Restore puts the most recent backup back and removes it.
func Restore(m *api.AdapterManifest, backupPath string) error {
	cfgPath := ExpandEnv(m.ConfigPath)
	if backupPath == "" {
		backupPath = cfgPath + ".woodgit-backup"
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("no backup at %s: %w", backupPath, err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return err
	}
	return os.Remove(backupPath)
}

func setPath(doc map[string]interface{}, keys []string, value string) {
	if len(keys) == 1 {
		doc[keys[0]] = value
		return
	}
	child, ok := doc[keys[0]].(map[string]interface{})
	if !ok {
		child = map[string]interface{}{}
		doc[keys[0]] = child
	}
	setPath(child, keys[1:], value)
}

// upsertEntries renders each manifest entry template ({{base_url}} /
// {{api_key}} placeholders in string values, nested included) and upserts it
// into the array at EntryArrayPath keyed by EntryKeyField. Existing array
// entries with other keys — e.g. models the user added by hand — are kept.
func upsertEntries(doc map[string]interface{}, detail *Manifest, baseURL, apiKey string) error {
	arrayPath := detail.EntryArrayPath
	if arrayPath == "" {
		arrayPath = "models"
	}
	keyField := detail.EntryKeyField
	if keyField == "" {
		keyField = "id"
	}
	keys := strings.Split(arrayPath, ".")

	parent := doc
	for _, k := range keys[:len(keys)-1] {
		child, ok := parent[k].(map[string]interface{})
		if !ok {
			child = map[string]interface{}{}
			parent[k] = child
		}
		parent = child
	}
	last := keys[len(keys)-1]
	arr, ok := parent[last].([]interface{})
	if !ok {
		arr = []interface{}{}
	}

	for _, template := range detail.Entries {
		renderedRaw := renderTemplate(template, baseURL, apiKey)
		rendered, ok := renderedRaw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("manifest entry template must be a JSON object")
		}
		keyValue, ok := rendered[keyField].(string)
		if !ok || keyValue == "" {
			return fmt.Errorf("manifest entry template must define a string %q field", keyField)
		}
		replaced := false
		for i, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if id, ok := m[keyField].(string); ok && id == keyValue {
				arr[i] = rendered
				replaced = true
				break
			}
		}
		if !replaced {
			arr = append(arr, rendered)
		}
	}
	parent[last] = arr
	return nil
}

func renderTemplate(v interface{}, baseURL, apiKey string) interface{} {
	switch t := v.(type) {
	case string:
		t = strings.ReplaceAll(t, "{{base_url}}", baseURL)
		return strings.ReplaceAll(t, "{{api_key}}", apiKey)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = renderTemplate(val, baseURL, apiKey)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = renderTemplate(val, baseURL, apiKey)
		}
		return out
	default:
		return v
	}
}
