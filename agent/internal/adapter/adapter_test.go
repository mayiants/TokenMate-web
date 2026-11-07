package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mayiants/ai-gateway-go/agent/internal/api"
)

func testManifest(t *testing.T, cfgPath string) *api.AdapterManifest {
	t.Helper()
	m := Manifest{
		Status:         "active",
		ConfigFormat:   "json",
		EntryArrayPath: "models",
		EntryKeyField:  "id",
		Entries: []map[string]interface{}{
			{"id": "glm-5.3", "url": "{{base_url}}/v1", "apiKey": "{{api_key}}", "vendor": "woodgit"},
		},
	}
	raw, _ := json.Marshal(m)
	return &api.AdapterManifest{Client: "workbuddy", OS: "linux", ConfigPath: cfgPath, InjectMethod: "file", Manifest: raw, Version: 1}
}

func readModels(t *testing.T, path string) []interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("config not object JSON: %v", err)
	}
	arr, _ := doc["models"].([]interface{})
	return arr
}

// Bare-array form (valid for the client) must be accepted and wrapped.
func TestApplyWrapsBareArray(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir, err := os.MkdirTemp(home, ".wbtest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfg := filepath.Join(dir, "models.json")
	if err := os.WriteFile(cfg, []byte(`[{"id":"deepseek-v4-pro","apiKey":"sk-x"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManifest(t, cfg)
	_, applied, err := Apply(m, "http://gw", "sk-test")
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v", applied, err)
	}
	models := readModels(t, cfg)
	if len(models) != 2 {
		t.Fatalf("want 2 models, got %d", len(models))
	}
	if models[0].(map[string]interface{})["id"] != "deepseek-v4-pro" {
		t.Fatalf("user entry not preserved first: %v", models[0])
	}
}

// The original-config backup must be written exactly once and never
// refreshed from a later (already-injected) state.
func TestBackupNeverOverwritten(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir, err := os.MkdirTemp(home, ".wbtest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfg := filepath.Join(dir, "models.json")
	original := []byte(`{"models":[{"id":"mine","apiKey":"sk-original"}]}`)
	if err := os.WriteFile(cfg, original, 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManifest(t, cfg)

	if _, applied, err := Apply(m, "http://gw", "sk-1"); err != nil || !applied {
		t.Fatalf("first apply: %v", err)
	}
	backup := cfg + ".woodgit-backup"
	bk, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(bk) != string(original) {
		t.Fatalf("backup != original: %s", bk)
	}

	// Second apply (re-key): backup must still hold the ORIGINAL bytes.
	if _, applied, err := Apply(m, "http://gw", "sk-2"); err != nil || !applied {
		t.Fatalf("second apply: %v", err)
	}
	bk2, _ := os.ReadFile(backup)
	if string(bk2) != string(original) {
		t.Fatal("backup was overwritten on re-injection")
	}
}

func TestApplyToleratesBOMAndTrailingCommas(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir, err := os.MkdirTemp(home, ".wbtest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfg := filepath.Join(dir, "models.json")
	doc := "\xef\xbb\xbf" + "{\"models\":[{\"id\":\"a\",\"x\":1,},],}"
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManifest(t, cfg)
	if _, applied, err := Apply(m, "http://gw", "sk-t"); err != nil || !applied {
		t.Fatalf("apply tolerant parse: %v", err)
	}
	if n := len(readModels(t, cfg)); n != 2 {
		t.Fatalf("want 2 models, got %d", n)
	}
}

func TestSafeConfigPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		path string
		ok   bool
	}{
		{filepath.Join(home, ".workbuddy", "models.json"), true},
		{"/etc/passwd", false},
		{"relative/models.json", false},
		{filepath.Join(home, "..", "evil.json"), false},
		{"/tmp/models.json", false},
	}
	for _, tc := range cases {
		err := safeConfigPath(tc.path)
		if tc.ok && err != nil {
			t.Errorf("%s: want ok, got %v", tc.path, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: want rejection, got nil", tc.path)
		}
	}
}

func TestUpsertReplacesSameIDOnly(t *testing.T) {
	doc := map[string]interface{}{"models": []interface{}{
		map[string]interface{}{"id": "glm-5.3", "apiKey": "old"},
		map[string]interface{}{"id": "other", "apiKey": "keep"},
	}}
	detail := &Manifest{EntryArrayPath: "models", EntryKeyField: "id",
		Entries: []map[string]interface{}{{"id": "glm-5.3", "apiKey": "new"}}}
	if err := upsertEntries(doc, detail, "http://gw", "new"); err != nil {
		t.Fatal(err)
	}
	arr := doc["models"].([]interface{})
	if len(arr) != 2 {
		t.Fatalf("want 2 entries, got %d", len(arr))
	}
	if arr[0].(map[string]interface{})["apiKey"] != "new" {
		t.Fatal("same-id entry not replaced")
	}
	if arr[1].(map[string]interface{})["apiKey"] != "keep" {
		t.Fatal("other entry not preserved")
	}
}

func TestFallbackManifest(t *testing.T) {
	m := FallbackManifest("workbuddy", "linux")
	if m == nil || !strings.Contains(m.ConfigPath, ".workbuddy") {
		t.Fatalf("linux fallback: %+v", m)
	}
	if FallbackManifest("workbuddy", "windows") == nil {
		t.Fatal("windows fallback nil")
	}
	if FallbackManifest("unknown-client", "linux") != nil {
		t.Fatal("unknown client should have no fallback")
	}
}
