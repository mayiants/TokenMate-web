package main

import (
	"github.com/mayiants/ai-gateway-go/agent/internal/device"
)

// osVersion reports a real OS version where the platform allows it
// ("win11-23H2 (build 22631.x)" on Windows, "linux"/"darwin" elsewhere),
// so the portal can tell Win10 from Win11 and track arch via GOARCH.
func osVersion() string { return device.OSVersion() }
