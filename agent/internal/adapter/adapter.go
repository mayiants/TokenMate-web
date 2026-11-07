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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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

// trailingCommaRe strips trailing commas (",}" / ",]") which WorkBuddy's
// parser tolerates but encoding/json rejects.
var trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)

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
	// Manifest list missed — fall back to runtime discovery (registry,
	// uninstall entries, fixed-drive scan). Auto-detected installs never
	// get version-probed: GUI clients must not be executed for detection.
	for _, p := range candidateInstallPaths() {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return true, "", nil
		}
	}
	return false, "", nil
}

func probeVersion(exe string, detail Manifest) string {
	// GUI clients (Electron) cannot be probed by executing them — the args
	// are ignored and the app window pops up. Only CLI-type clients should
	// declare version_args in the manifest.
	if len(detail.VersionArgs) == 0 {
		return ""
	}
	cmd := exec.Command(exe, detail.VersionArgs...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return ""
	}
	// Cap the probe so a client that stays running never blocks detection.
	timer := time.AfterFunc(5*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()
	_ = cmd.Wait()
	s := strings.TrimSpace(buf.String())
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
		// %VAR% form (CMD/PowerShell convention) — os.Expand only knows $VAR.
		for {
			start := strings.IndexByte(s, '%')
			if start == -1 {
				break
			}
			rest := s[start+1:]
			end := strings.IndexByte(rest, '%')
			if end <= 0 {
				break
			}
			s = s[:start] + os.Getenv(rest[:end]) + rest[end+1:]
		}
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
	if err := safeConfigPath(cfgPath); err != nil {
		return "", false, err
	}
	raw, readErr := os.ReadFile(cfgPath)
	exists := readErr == nil

	var doc map[string]interface{}
	if exists {
		parsed := bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
		if len(bytes.TrimSpace(parsed)) == 0 {
			doc = map[string]interface{}{}
		} else {
			// WorkBuddy's parser tolerates trailing commas; encoding/json
			// does not. Normalize before unmarshalling.
			parsed = trailingCommaRe.ReplaceAll(parsed, []byte("$1"))
			// models.json may be a bare array or an object holding a
			// "models" key — both forms are valid for the client.
			var decoded interface{}
			if err := json.Unmarshal(parsed, &decoded); err != nil {
				preview := bytes.TrimSpace(parsed)
				if len(preview) > 64 {
					preview = preview[:64]
				}
				return "", false, fmt.Errorf("existing config %s is not valid JSON (first bytes %s): %w", cfgPath, strconv.Quote(string(preview)), err)
			}
			switch v := decoded.(type) {
			case map[string]interface{}:
				doc = v
			case []interface{}:
				doc = map[string]interface{}{"models": v}
			default:
				doc = map[string]interface{}{}
			}
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
		if _, err := os.Stat(backupPath); errors.Is(err, os.ErrNotExist) {
			// First injection only: capture the user's ORIGINAL config. The
			// backup must never be refreshed from a later (already-injected)
			// state, or restore would return our own key instead of the
			// user's original file.
			stamped := fmt.Sprintf("%s.woodgit-backup-%s", cfgPath, time.Now().Format("20060102150405"))
			if err := os.WriteFile(stamped, raw, 0o600); err != nil {
				return "", false, fmt.Errorf("backup config: %w", err)
			}
			if err := os.WriteFile(backupPath, raw, 0o600); err != nil {
				return "", false, fmt.Errorf("backup config: %w", err)
			}
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
		if !ok || keyValue == "" || len(keyValue) > 128 {
			return fmt.Errorf("manifest entry template must define a string %q field of 1-128 chars", keyField)
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

// safeConfigPath guards the single filesystem write this package performs.
// The manifest is data, not code: without this, a portal misconfiguration or
// compromise would become an arbitrary-file-write primitive on customer
// machines. The resolved path must be absolute, free of "..", and live
// inside well-known user-profile roots.
func safeConfigPath(p string) error {
	if p == "" {
		return fmt.Errorf("config path is empty")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("config path %q is not absolute", p)
	}
	clean := filepath.Clean(p)
	if strings.Contains(clean, "..") {
		return fmt.Errorf("config path %q contains '..'", p)
	}
	var allowed []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		allowed = append(allowed, filepath.Clean(home))
	}
	for _, env := range []string{"LOCALAPPDATA", "APPDATA", "XDG_CONFIG_HOME"} {
		if v := os.Getenv(env); v != "" {
			allowed = append(allowed, filepath.Clean(v))
		}
	}
	for _, root := range allowed {
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("config path %q is outside the allowed user-profile roots", p)
}

// ConfigDirExists reports whether the adapter's target config directory is
// present. A client whose config dir exists counts as installed even when
// the executable itself is outside every known install location.
func ConfigDirExists(m *api.AdapterManifest) bool {
	if m == nil || m.ConfigPath == "" {
		return false
	}
	info, err := os.Stat(filepath.Dir(ExpandEnv(m.ConfigPath)))
	return err == nil && info.IsDir()
}

// FallbackManifest returns a built-in manifest for known clients so the
// agent can still inject (and doctor can still verify) when the portal is
// unreachable or has no adapter row. Paths match the P0-verified layout.
func FallbackManifest(client, osName string) *api.AdapterManifest {
	if client != "workbuddy" {
		return nil
	}
	osName = strings.ToLower(osName)
	var configPath string
	switch osName {
	case "windows":
		configPath = `%USERPROFILE%\.workbuddy\models.json`
	case "linux", "darwin":
		configPath = "$HOME/.workbuddy/models.json"
	default:
		return nil
	}
	manifest := Manifest{
		Status:         "active",
		ConfigFormat:   "json",
		EntryArrayPath: "models",
		EntryKeyField:  "id",
		Entries: []map[string]interface{}{
			{"id": "glm-5.3", "name": "GLM 5.3 (Woodgit)", "vendor": "woodgit",
				"url": "{{base_url}}/v1", "apiKey": "{{api_key}}",
				"supportsToolCall": true, "supportsImages": true, "supportsReasoning": true, "useCustomProtocol": false},
			{"id": "kimi-k3", "name": "Kimi K3 (Woodgit)", "vendor": "woodgit",
				"url": "{{base_url}}/v1", "apiKey": "{{api_key}}",
				"supportsToolCall": true, "supportsImages": true, "supportsReasoning": true, "useCustomProtocol": false},
		},
	}
	raw, _ := json.Marshal(manifest)
	return &api.AdapterManifest{
		Client:       client,
		OS:           osName,
		ConfigPath:   configPath,
		InjectMethod: "file",
		Manifest:     raw,
		Version:      -1, // signals "local fallback, not a portal version"
	}
}
