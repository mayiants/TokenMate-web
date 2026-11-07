//go:build windows

package device

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

// platformSeed returns the machine GUID so the fingerprint survives agent
// reinstalls. Falls back to empty (random-only fingerprint) if the registry
// key is unreadable under a restricted account.
func platformSeed() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err != nil {
		return "noguid"
	}
	defer k.Close()
	guid, _, err := k.GetStringValue("MachineGuid")
	if err != nil || guid == "" {
		return "noguid"
	}
	return guid
}

// OSVersion distinguishes Win10 from Win11 and reports the feature update
// and build number, e.g. "win11-23H2 (build 22631.4167)". Falls back to
// "windows" when the version key is unreadable.
func OSVersion() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "windows"
	}
	defer k.Close()
	build, _, _ := k.GetStringValue("CurrentBuildNumber")
	display, _, _ := k.GetStringValue("DisplayVersion")
	if display == "" {
		display, _, _ = k.GetStringValue("ReleaseId")
	}
	ubr, _, _ := k.GetIntegerValue("UBR")
	if build == "" {
		return "windows"
	}
	rel := "win10"
	if b, err := strconv.Atoi(build); err == nil && b >= 22000 {
		rel = "win11"
	}
	ver := rel
	if display != "" {
		ver = rel + "-" + display
	}
	if ubr > 0 {
		ver = fmt.Sprintf("%s (build %s.%d)", ver, build, ubr)
	}
	return ver
}
