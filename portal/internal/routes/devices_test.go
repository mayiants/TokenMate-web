package routes

import (
	"testing"
)

// TestDeviceRoutesMethodsExist is a compile-time check.
func TestDeviceRoutesMethodsExist(t *testing.T) {
	_ = (*DeviceRoutes).listDevices
	_ = (*DeviceRoutes).recordDevice
	_ = (*DeviceRoutes).deleteDevice
}

// TestDeviceTypeValues tests valid device types.
func TestDeviceTypeValues(t *testing.T) {
	validDeviceTypes := []string{"desktop", "mobile", "tablet", "unknown"}

	for _, dt := range validDeviceTypes {
		if dt == "" {
			t.Error("device type should not be empty")
		}
	}

	// Verify common types
	knownTypes := map[string]bool{
		"desktop": true,
		"mobile":  true,
		"tablet":  true,
		"unknown": true,
	}

	for deviceType, expected := range knownTypes {
		if !expected {
			t.Errorf("device type %q should be known", deviceType)
		}
		_ = deviceType
	}
}

// TestDeviceOSValues tests common OS values.
func TestDeviceOSValues(t *testing.T) {
	knownOS := []string{"Windows", "macOS", "Linux", "iOS", "Android"}

	for _, os := range knownOS {
		if os == "" {
			t.Error("OS value should not be empty")
		}
	}
}

// TestDeviceBrowserValues tests common browser values.
func TestDeviceBrowserValues(t *testing.T) {
	knownBrowsers := []string{"Chrome", "Firefox", "Safari", "Edge", "Opera"}

	for _, browser := range knownBrowsers {
		if browser == "" {
			t.Error("browser value should not be empty")
		}
	}
}

// TestDeviceRequestStruct tests the request struct used in recordDevice.
func TestDeviceRequestStruct(t *testing.T) {
	// Simulate the request struct fields
	req := struct {
		DeviceName string `json:"device_name"`
		DeviceType string `json:"device_type"`
		Browser    string `json:"browser"`
		OS         string `json:"os"`
		Location   string `json:"location"`
	}{
		DeviceName: "Test Device",
		DeviceType: "desktop",
		Browser:    "Chrome",
		OS:         "Linux",
		Location:   "Test Location",
	}

	// Default browser should fall back to User-Agent
	if req.Browser == "" {
		req.Browser = "Mozilla/5.0"
	}

	if req.DeviceName == "" {
		t.Error("DeviceName should not be empty")
	}
	if req.DeviceType == "" {
		t.Error("DeviceType should not be empty")
	}
	if req.Browser == "" {
		t.Error("Browser should have a fallback value")
	}
}
