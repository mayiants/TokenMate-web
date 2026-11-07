package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mayiants/ai-gateway-go/agent/internal/adapter"
	"github.com/mayiants/ai-gateway-go/agent/internal/api"
	"github.com/mayiants/ai-gateway-go/agent/internal/creds"
	"github.com/mayiants/ai-gateway-go/agent/internal/device"
	"github.com/mayiants/ai-gateway-go/agent/internal/state"
	"github.com/spf13/cobra"
)

// loadEnrolled reads state + secrets for commands that require enrollment.
func loadEnrolled() (*state.State, *api.Client, string, string, error) {
	s, err := state.Load()
	if err != nil {
		return nil, nil, "", "", err
	}
	key, err := creds.GetAPIKey()
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("api key unreadable: %w", err)
	}
	fp, err := device.Fingerprint()
	if err != nil {
		return nil, nil, "", "", err
	}
	return s, api.NewClient(s.Server, s.InsecureTLS), key, fp, nil
}

func newHeartbeatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "heartbeat",
		Short: "Single liveness report to the portal (run periodically via Task Scheduler)",
		Long: "One-shot heartbeat: refreshes device liveness, learns key revocation, and " +
			"re-applies the client adapter when the portal publishes a newer manifest. " +
			"Exits non-zero when the issued key has been revoked.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, c, key, fp, err := loadEnrolled()
			if err != nil {
				return err
			}
			ctx := context.Background()
			hb, err := c.Heartbeat(ctx, key, map[string]string{
				"device_id":          s.DeviceID,
				"device_fingerprint": fp,
				"agent_version":      Version,
				"client_version":     s.ClientVersion,
			})
			if err != nil {
				logf("heartbeat failed: %v", err)
				return err
			}
			if hb.KeyStatus != "active" {
				logf("heartbeat: key status %s", hb.KeyStatus)
				fmt.Printf("KEY STATUS: %s — this device has been revoked; contact your administrator.\n", hb.KeyStatus)
				return fmt.Errorf("key revoked")
			}
			fmt.Printf("ok (key active, config v%d)\n", hb.ConfigVersion)

			if hb.ConfigVersion > s.AdapterVersion {
				m, err := c.GetConfig(ctx, s.AdapterClient, runtime.GOOS, runtime.GOARCH)
				if err != nil {
					// Portal unreachable: fall back to the built-in manifest so
					// a fresh machine can still finish (or repair) injection.
					m = adapter.FallbackManifest(s.AdapterClient, runtime.GOOS)
					logf("GetConfig failed (%v); using built-in fallback manifest", err)
					if m == nil {
						return nil
					}
				}
				if adapter.Pending(m) {
					return nil
				}
				backup, applied, err := adapter.Apply(m, s.APIBaseURL, key)
				if err != nil {
					logf("adapter re-apply failed: %v", err)
					fmt.Printf("WARN: adapter re-apply failed: %v\n", err)
					return nil
				}
				if applied {
					s.AdapterBackupPath = backup
					if m.Version >= 0 {
						s.AdapterVersion = m.Version
					}
					_ = s.Save()
					fmt.Printf("client config updated to adapter v%d (backup %s)\n", m.Version, backup)
				}
			}
			return nil
		},
	}
}

func newDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Self-check: enrollment, key, server reachability, client config",
		RunE: func(cmd *cobra.Command, args []string) error {
			failures := 0
			check := func(name string, ok bool, detail string) {
				mark := "PASS"
				if !ok {
					mark = "FAIL"
					failures++
				}
				fmt.Printf("%-5s %-28s %s\n", mark, name, detail)
			}

			s, c, key, fp, err := loadEnrolled()
			if err != nil {
				check("enrolled", false, err.Error())
				return fmt.Errorf("%d check(s) failed", failures)
			}
			check("state file", true, "present")
			check("api key store", true, s.KeyPrefix)

			ctx := context.Background()
			m, err := c.GetConfig(ctx, s.AdapterClient, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				check("portal reachable", false, err.Error())
			} else {
				check("portal reachable", true, fmt.Sprintf("adapter manifest v%d", m.Version))
			}

			hb, err := c.Heartbeat(ctx, key, map[string]string{
				"device_id":          s.DeviceID,
				"device_fingerprint": fp,
				"agent_version":      Version,
				"client_version":     s.ClientVersion,
			})
			if err != nil {
				check("heartbeat", false, err.Error())
			} else {
				check("heartbeat", hb.KeyStatus == "active", "key_status="+hb.KeyStatus)
			}

			if m != nil {
				installed, version, derr := adapter.Detect(m)
				if derr != nil {
					check("client detect", false, derr.Error())
				} else if !installed {
					// The exe may sit outside every known location (custom
					// portable installs); the config dir is what injection
					// actually needs.
					if adapter.ConfigDirExists(m) {
						check("client detect", true, "config dir present (exe not located)")
					} else {
						check("client detect", false, s.AdapterClient+" not found on this machine")
					}
				} else {
					check("client detect", true, version)
					if version != "" && version != s.ClientVersion {
						s.ClientVersion = version
						_ = s.Save()
					}
				}
				switch {
				case adapter.Pending(m):
					check("config injection", true, "adapter pending platform verification (expected)")
				case !installed:
					check("config injection", false, "client not installed")
				default:
					cfgPath := adapter.ExpandEnv(m.ConfigPath)
					ok, detail := verifyConfig(cfgPath, s.APIBaseURL, key)
					check("config injection", ok, detail)
					if !ok && fix {
						if _, applied, aerr := adapter.Apply(m, s.APIBaseURL, key); aerr == nil && applied {
							fmt.Println("      re-injected on request (--fix)")
						}
					}
				}
			}

			if failures > 0 {
				return fmt.Errorf("%d check(s) failed", failures)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "re-inject config when the injection check fails")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Restore the client config from the pre-injection backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, c, _, _, err := loadEnrolled()
			if err != nil {
				return err
			}
			m, err := c.GetConfig(context.Background(), s.AdapterClient, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				return err
			}
			if err := adapter.Restore(m, s.AdapterBackupPath); err != nil {
				return err
			}
			fmt.Println("Client config restored from backup.")
			return nil
		},
	}
}

func detectClientVersion(s *state.State) string {
	c := api.NewClient(s.Server, s.InsecureTLS)
	m, err := c.GetConfig(context.Background(), s.AdapterClient, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ""
	}
	installed, version, err := adapter.Detect(m)
	if err != nil || !installed {
		return ""
	}
	return version
}

// shortKey never panics on short keys (a key shorter than the display
// width is still a valid key).
func shortKey(key string) string {
	if len(key) <= 15 {
		return key
	}
	return key[:15]
}

// logf appends a timestamped line to the agent log inside the state dir so
// unattended failures (e.g. scheduled heartbeats) leave a trace.
func logf(format string, args ...interface{}) {
	f, err := os.OpenFile(filepath.Join(state.Dir(), "agent.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func verifyConfig(cfgPath, baseURL, apiKey string) (bool, string) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return false, "config missing: " + err.Error()
	}
	// The client accepts a bare array or an object (and may write a BOM) —
	// validity here must match that tolerance.
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, "config not valid JSON"
	}
	// Full field verification needs the manifest's dotted paths; the cheap
	// check (file exists, parses, mentions our gateway host) catches the
	// common breakage modes (deleted file, overwritten by client upgrade).
	if !strings.Contains(string(raw), strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")) {
		return false, "config does not reference gateway " + baseURL
	}
	if !strings.Contains(string(raw), shortKey(apiKey)) {
		return false, "config does not contain this device's key"
	}
	return true, "gateway + key present"
}
