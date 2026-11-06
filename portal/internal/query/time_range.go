package query

// TimeRange describes a dashboard time-range selection.
type TimeRange struct {
	Interval string // SQL interval for reference/performance lower bound
	Bucket   string // generate_series step, e.g. "4 hours", "1 day", "1 month"
	Buckets  int    // number of buckets to generate
	Format   string // TO_CHAR format for the date part
	LabelKey string // i18n key for the button label
}

// timeRanges defines all supported time ranges.
var timeRanges = map[string]TimeRange{
	"1d":      {Interval: "1 day", Bucket: "4 hours", Buckets: 6, Format: "YYYY-MM-DD", LabelKey: "1day"},
	"7d":      {Interval: "7 days", Bucket: "1 day", Buckets: 7, Format: "YYYY-MM-DD", LabelKey: "7days"},
	"15d":     {Interval: "15 days", Bucket: "1 day", Buckets: 15, Format: "YYYY-MM-DD", LabelKey: "15days"},
	"30d":     {Interval: "30 days", Bucket: "1 day", Buckets: 30, Format: "YYYY-MM-DD", LabelKey: "30days"},
	"quarter": {Interval: "3 months", Bucket: "1 month", Buckets: 3, Format: "YYYY-MM-DD", LabelKey: "quarter"},
}

// DefaultRange is the default time range when none is specified.
const DefaultRange = "30d"

// AllowedRanges enumerates the supported shortcut keys.
var AllowedRanges = map[string]bool{
	"1d":      true,
	"7d":      true,
	"15d":     true,
	"30d":     true,
	"quarter": true,
}
