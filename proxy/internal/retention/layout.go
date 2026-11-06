package retention

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	tenantRoot            = "ent"
	personalTenant        = "_personal"
	defaultMaxDiskPercent = 90
	maxRetentionDays      = 365
	accessLogName         = "retention-access.log"
)

// ResolveTenant maps an enterprise id to its path relative to the retention
// directory. Only canonical (lower-case, hyphenated) UUIDs and "_personal" are
// accepted: the value ends up in a file path, so anything else - including
// "../../x", URN/braced UUID forms and mixed case aliases - is rejected.
func ResolveTenant(enterpriseID string) (string, bool) {
	if enterpriseID == personalTenant {
		return filepath.Join(tenantRoot, personalTenant), true
	}
	id := strings.ToLower(enterpriseID)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", false
	}
	return filepath.Join(tenantRoot, id), true
}

// tenantRelDir is ResolveTenant with the safe fallback for writes: invalid or
// empty ids are stored under _personal rather than dropped.
func tenantRelDir(enterpriseID string) string {
	if rel, ok := ResolveTenant(enterpriseID); ok {
		return rel
	}
	return filepath.Join(tenantRoot, personalTenant)
}

func dateRelDir(t time.Time) string {
	return filepath.Join(
		fmt.Sprintf("%04d", t.Year()),
		fmt.Sprintf("%02d", t.Month()),
		fmt.Sprintf("%02d", t.Day()),
		fmt.Sprintf("%02d", t.Hour()),
	)
}

// daysDirName returns "<days>d", falling back to the store's max age when the
// record carries no (or an out-of-range) policy.
func (s *DiskStore) daysDirName(days int) string {
	if days <= 0 {
		days = int((s.maxAge + 24*time.Hour - 1) / (24 * time.Hour))
	}
	if days < 1 {
		days = 1
	}
	if days > maxRetentionDays {
		days = maxRetentionDays
	}
	return fmt.Sprintf("%dd", days)
}

// safeFileComponent reports whether id can be used as a single path element.
func safeFileComponent(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, "/\\\x00") && filepath.Base(id) == id
}
