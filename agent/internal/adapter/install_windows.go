//go:build windows

package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// candidateInstallPaths discovers WorkBuddy install locations at runtime so
// the manifest's static list is only a fallback. Compatible with Win10/11,
// 64-bit and 32-bit (WOW6432Node) installer views, per-user and machine
// installs, and non-default install roots (e.g. D:\WorkBuddy).
func candidateInstallPaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p == "" || seen[strings.ToLower(p)] {
			return
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}

	// 1) App Paths registration (classic installer mechanism).
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\WorkBuddy.exe`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue(""); err == nil {
			add(v)
		}
		_ = k.Close()
	}

	// 2) Uninstall entries: machine + user hives, 64-bit + WOW6432Node views.
	uninstallHives := [][2]string{
		{"LM", `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{"LM", `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{"CU", `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{"CU", `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	for _, hive := range uninstallHives {
		root := registry.LOCAL_MACHINE
		if hive[0] == "CU" {
			root = registry.CURRENT_USER
		}
		k, err := registry.OpenKey(root, hive[1], registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, err := k.ReadSubKeyNames(-1)
		if err == nil {
			for _, name := range names {
				sk, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
				if err != nil {
					continue
				}
				dn, _, err := sk.GetStringValue("DisplayName")
				if err == nil && strings.Contains(strings.ToLower(dn), "workbuddy") {
					if loc, _, err := sk.GetStringValue("InstallLocation"); err == nil && strings.TrimSpace(loc) != "" {
						add(filepath.Join(strings.Trim(loc, `"`), "WorkBuddy.exe"))
					}
					if icon, _, err := sk.GetStringValue("DisplayIcon"); err == nil && strings.TrimSpace(icon) != "" {
						if i := strings.LastIndex(icon, ","); i > 0 && len(icon)-i <= 3 {
							icon = icon[:i]
						}
						if strings.HasSuffix(strings.ToLower(strings.Trim(icon, `"`)), ".exe") {
							add(icon)
						}
					}
				}
				_ = sk.Close()
			}
		}
		_ = k.Close()
	}

	// 3) Bounded drive scan on fixed disks (covers portable/custom roots).
	if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
		add(filepath.Join(localApp, "Programs", "WorkBuddy", "WorkBuddy.exe"))
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDriveType := kernel32.NewProc("GetDriveTypeW")
	for _, d := range logicalDrives() {
		rootPath := d + `:\`
		t, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(rootPath))))
		if t != 3 { // DRIVE_FIXED
			continue
		}
		add(filepath.Join(rootPath, "WorkBuddy", "WorkBuddy.exe"))
		add(filepath.Join(rootPath, "Program Files", "WorkBuddy", "WorkBuddy.exe"))
		add(filepath.Join(rootPath, "Program Files (x86)", "WorkBuddy", "WorkBuddy.exe"))
	}
	return out
}

func logicalDrives() []string {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getLogicalDrives := kernel32.NewProc("GetLogicalDrives")
	r, _, _ := getLogicalDrives.Call()
	if r == 0 {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if r&(1<<uint(i)) != 0 {
			out = append(out, string(rune('A'+i)))
		}
	}
	return out
}
