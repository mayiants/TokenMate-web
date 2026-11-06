package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// State is the agent's local enrollment record. It holds nothing secret —
// the api key lives in the OS credential store (creds package).
type State struct {
	Server            string `json:"server"`              // portal URL used at enroll time
	InsecureTLS       bool   `json:"insecure_tls"`        // test deployments with self-signed certs
	APIBaseURL        string `json:"api_base_url"`        // gateway URL written into client configs
	DeviceID          string `json:"device_id"`
	KeyID             string `json:"key_id"`
	KeyPrefix         string `json:"key_prefix"`
	MemberName        string `json:"member_name"`
	MemberEmail       string `json:"member_email"`
	Hostname          string `json:"hostname"`
	AdapterClient     string `json:"adapter_client"`
	AdapterVersion    int    `json:"adapter_version"`
	AdapterBackupPath string `json:"adapter_backup_path,omitempty"`
	EnrolledAt        string `json:"enrolled_at"`
}

func Dir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "WoodgitAgent")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".woodgit-agent")
}

func path() string { return filepath.Join(Dir(), "state.json") }

func Load() (*State, error) {
	raw, err := os.ReadFile(path())
	if err != nil {
		return nil, fmt.Errorf("not enrolled (state file %s missing): %w", path(), err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("corrupt state file: %w", err)
	}
	return &s, nil
}

func (s *State) Save() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(), raw, 0o600)
}

func Clear() error {
	err := os.Remove(path())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
