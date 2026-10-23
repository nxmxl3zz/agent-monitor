package core

import "time"

// MonitorAnalytics contains only deduplicated usage aggregates, without transcripts.
type MonitorAnalytics struct {
	Range         string              `json:"range"`
	Since         time.Time           `json:"since"`
	Until         time.Time           `json:"until"`
	BucketSeconds int64               `json:"bucket_seconds"`
	Points        []MonitorUsagePoint `json:"points"`
}

type MonitorUsagePoint struct {
	At         int64  `json:"at"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
	Total      int64  `json:"total"`
	Requests   int64  `json:"requests"`
}

func MonitorRangeDuration(value string) time.Duration {
	switch value {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 0
}
