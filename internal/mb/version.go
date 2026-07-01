package mb

import "strings"

var (
	appVersion = "dev"
	appCommit  = ""
)

func displayVersion() string {
	v := strings.TrimSpace(appVersion)
	if v == "" {
		return "dev"
	}
	return v
}

func displayCommit() string {
	c := strings.TrimSpace(appCommit)
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
