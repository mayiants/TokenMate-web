package query

import (
	"context"
	"log"
)

// logQueryError records a swallowed query failure. Many query helpers in this
// package return empty data + nil error on DB failure so the page still
// renders (rather than 500-ing the whole admin dashboard). That hides real
// outages; this helper emits a server-side error log so failures surface in
// the logs without changing the caller-facing return signature.
//
// ctx is accepted so future structured-logging wiring can attach a trace id.
func logQueryError(ctx context.Context, where string, err error) {
	if err == nil {
		return
	}
	log.Printf("query error in %s: %v", where, err)
}
