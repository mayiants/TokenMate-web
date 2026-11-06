package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	root := newRootCmd()
	root.AddCommand(
		newEnrollCmd(),
		newStatusCmd(),
		newDoctorCmd(),
		newHeartbeatCmd(),
		newRestoreCmd(),
		newVersionCmd(),
	)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "woodgit-agent",
		Short: "Woodgit enterprise AI access agent",
		Long: "Enrolls this PC into a Woodgit enterprise workspace: obtains a device-bound " +
			"sub-key via email OTP, injects it into supported AI clients (workbuddy), and " +
			"reports liveness via heartbeat.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print agent version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("woodgit-agent %s (built %s)\n", Version, BuildTime)
		},
	}
}
