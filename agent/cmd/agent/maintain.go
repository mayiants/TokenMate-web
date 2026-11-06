package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
				"client_version":     detectClientVersion(s),
			})
			if err != nil {
				return err
			}
			if hb.KeyStatus != "active" {
				fmt.Printf("KEY STATUS: %s — this device has been revoked; contact your administrator.\n", hb.KeyStatus)
				return fmt.Errorf("key revoked")
			}
			fmt.Printf("ok (key active, config v%d)\n", hb.ConfigVersion)

			if hb.ConfigVersion > s.AdapterVersion {
				m, err := c.GetConfig(ctx, s.AdapterClient, "windows")
				if err != nil {
					fmt.Printf("WARN: adapter manifest fetch failed: %v\n", err)
					return nil
				}
				if adapter.Pending(m) {
					return nil
				}
				backup, applied, err := adapter.Apply(m, s.APIBaseURL, key)
				if err != nil {
					fmt.Printf("WARN: adapter re-apply failed: %v\n", err)
					return nil
				}
				if applied {
					s.AdapterBackupPath = backup
					s.AdapterVersion = m.Version
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
			m, err := c.GetConfig(ctx, s.AdapterClient, "windows")
			if err != nil {
				check("portal reachable", false, err.Error())
			} else {
				check("portal reachable", true, fmt.Sprintf("adapter manifest v%d", m.Version))
			}

			hb, err := c.Heartbeat(ctx, key, map[string]string{
				"device_id":          s.DeviceID,
				"device_fingerprint": fp,
				"agent_version":      Version,
				"client_version":     detectClientVersion(s),
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
					check("client detect", false, s.AdapterClient+" not found on this machine")
				} else {
					check("client detect", true, version)
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
			m, err := c.GetConfig(context.Background(), s.AdapterClient, "windows")
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
	m, err := c.GetConfig(context.Background(), s.AdapterClient, "windows")
	if err != nil {
		return ""
	}
	installed, version, err := adapter.Detect(m)
	if err != nil || !installed {
		return ""
	}
	return version
}

func verifyConfig(cfgPath, baseURL, apiKey string) (bool, string) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return false, "config missing: " + err.Error()
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, "config not valid JSON"
	}
	_ = doc
	// Full field verification needs the manifest's dotted paths; the cheap
	// check (file exists, parses, mentions our gateway host) catches the
	// common breakage modes (deleted file, overwritten by client upgrade).
	if !strings.Contains(string(raw), strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")) {
		return false, "config does not reference gateway " + baseURL
	}
	if !strings.Contains(string(raw), apiKey[:15]) {
		return false, "config does not contain this device's key"
	}
	return true, "gateway + key present"
}
