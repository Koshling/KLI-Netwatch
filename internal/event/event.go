package event

import "time"

type AccessEvent struct {
	Time          time.Time
	ClientIP      string
	Method        string
	URL           string
	Protocol      string
	Status        int
	ResponseSize  int64
	Referer       string
	UserAgent     string
	XForwardedFor string
}

type ErrorEvent struct {
	Time       time.Time
	Severity   string
	PID        int
	Connection string
	Message    string
}
