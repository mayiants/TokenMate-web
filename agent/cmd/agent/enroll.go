package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mayiants/ai-gateway-go/agent/internal/adapter"
	"github.com/mayiants/ai-gateway-go/agent/internal/api"
	"github.com/mayiants/ai-gateway-go/agent/internal/creds"
	"github.com/mayiants/ai-gateway-go/agent/internal/device"
	"github.com/mayiants/ai-gateway-go/agent/internal/state"
	"github.com/spf13/cobra"
)

func newEnrollCmd() *cobra.Command {
	var (
		server   string
		insecure bool
		email    string
		code     string
		client   string
		groupID  string
	)
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll this PC into a Woodgit enterprise workspace",
		Long: "Verifies your corporate email with a one-time code, exchanges it for a " +
			"device-bound sub-key, and injects the key into supported AI clients.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if server == "" {
				return fmt.Errorf("--server is required (portal URL, e.g. https://api.example.com)")
			}
			ctx := context.Background()
			c := api.NewClient(server, insecure)
			reader := bufio.NewReader(os.Stdin)

			ask := func(flag *string, prompt string) (string, error) {
				if *flag != "" {
					return *flag, nil
				}
				fmt.Print(prompt)
				line, err := reader.ReadString('\n')
				if err != nil {
					return "", fmt.Errorf("read input: %w", err)
				}
				return strings.TrimSpace(line), nil
			}

			var err error
			email, err = ask(&email, "Work email: ")
			if err != nil || email == "" {
				return fmt.Errorf("email required")
			}
			if err := c.SendOTP(ctx, email); err != nil {
				return err
			}
			fmt.Println("Verification code sent to your mailbox (valid 10 minutes).")

			code, err = ask(&code, "Code: ")
			if err != nil || code == "" {
				return fmt.Errorf("code required")
			}
			verified, err := c.VerifyOTP(ctx, email, code)
			if err != nil {
				return err
			}
			fmt.Printf("Identity verified: %s\n", verified.MemberName)

			fp, err := device.Fingerprint()
			if err != nil {
				return err
			}
			hostname, _ := os.Hostname()
			req := map[string]string{
				"session_token":      verified.SessionToken,
				"device_fingerprint": fp,
				"hostname":           hostname,
				"os_version":         osVersion(),
				"agent_version":      Version,
				"client":             client,
				"os":                 "windows",
			}
			if groupID != "" {
				req["group_id"] = groupID
			}
			exchanged, err := c.Exchange(ctx, req)
			if err != nil {
				return err
			}

			if err := creds.PutAPIKey(exchanged.APIKey); err != nil {
				return fmt.Errorf("store api key: %w", err)
			}

			s := &state.State{
				Server:         server,
				InsecureTLS:    insecure,
				APIBaseURL:     exchanged.APIBaseURL,
				DeviceID:       exchanged.DeviceID,
				KeyID:          exchanged.KeyID,
				KeyPrefix:      exchanged.APIKey[:15] + "...",
				MemberName:     verified.MemberName,
				MemberEmail:    email,
				Hostname:       hostname,
				AdapterClient:  client,
				AdapterVersion: 0,
				EnrolledAt:     time.Now().UTC().Format(time.RFC3339),
			}
			if err := s.Save(); err != nil {
				return fmt.Errorf("save state: %w", err)
			}

			fmt.Printf("Enrolled. Key %s issued for device %s.\n", s.KeyPrefix, exchanged.DeviceID)

			if exchanged.Adapter == nil {
				fmt.Println("No adapter manifest returned; key stored, nothing to inject.")
				return nil
			}
			if adapter.Pending(exchanged.Adapter) {
				fmt.Println("Client adapter is pending on-platform verification — key stored, config injection skipped.")
				fmt.Println("Run `woodgit-agent doctor` after the adapter is activated to finish setup.")
				return nil
			}
			backup, applied, err := adapter.Apply(exchanged.Adapter, exchanged.APIBaseURL, exchanged.APIKey)
			if err != nil {
				fmt.Printf("WARN: config injection failed: %v\n", err)
				fmt.Println("The key is stored; re-run `woodgit-agent doctor` after fixing the issue.")
				return nil
			}
			if applied {
				s.AdapterBackupPath = backup
				s.AdapterVersion = exchanged.Adapter.Version
				_ = s.Save()
				fmt.Printf("Client config injected (backup at %s).\n", backup)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "portal base URL")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "skip TLS verification (test deployments with self-signed certs)")
	cmd.Flags().StringVar(&email, "email", "", "work email (skips prompt)")
	cmd.Flags().StringVar(&code, "code", "", "6-digit OTP (skips prompt)")
	cmd.Flags().StringVar(&client, "client", "workbuddy", "client to configure")
	cmd.Flags().StringVar(&groupID, "group-id", "", "enterprise policy group (optional)")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show enrollment state",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := state.Load()
			if err != nil {
				return err
			}
			fmt.Printf("Enrolled:    %s\n", s.MemberName)
			fmt.Printf("Email:       %s\n", s.MemberEmail)
			fmt.Printf("Server:      %s\n", s.Server)
			fmt.Printf("Gateway:     %s\n", s.APIBaseURL)
			fmt.Printf("Key:         %s (id %s)\n", s.KeyPrefix, s.KeyID)
			fmt.Printf("Device:      %s (hostname %s)\n", s.DeviceID, s.Hostname)
			fmt.Printf("Client:      %s (adapter manifest v%d)\n", s.AdapterClient, s.AdapterVersion)
			fmt.Printf("Enrolled at: %s\n", s.EnrolledAt)
			return nil
		},
	}
}

func osVersion() string {
	// Detailed OS version (build number etc.) is probed on Windows via the
	// registry in a later iteration; the server mainly needs coarse shape.
	return runtimeGOOS()
}
