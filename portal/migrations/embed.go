// Package migrations exposes SQL files that must also run from InitSchema.
//
// Most migrations are applied from disk by db.Migrate. The audit tamper-evidence
// DDL (0034) is additionally embedded so InitSchema can install the identical
// triggers/functions on fresh databases and in tests without keeping a second
// hand-copied version that could drift from the migration file.
package migrations

import _ "embed"

// AuditTamperEvidenceSQL is migrations/0034_audit_tamper_evidence.sql.
//
//go:embed 0034_audit_tamper_evidence.sql
var AuditTamperEvidenceSQL string
