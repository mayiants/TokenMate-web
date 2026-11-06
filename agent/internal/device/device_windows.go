//go:build windows

package device

import (
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
