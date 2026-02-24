package loggen

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"
)

// GetAppProfile returns the AppProfile for the given scenario path, or nil if unknown.
func GetAppProfile(path string) *AppProfile {
	path = strings.TrimPrefix(path, "builtin/")
	switch path {
	case "k8s-nginx":
		return NginxProfile()
	case "k8s-mysql":
		return MySQLProfile()
	case "k8s-redis":
		return RedisProfile()
	case "k8s-goapp":
		return GoAppProfile()
	default:
		return nil
	}
}

// GenericProfile returns a simple profile for unknown paths (fallback).
func GenericProfile(serviceName string) *AppProfile {
	tsLayout := "2006-01-02T15:04:05.000Z"
	return &AppProfile{
		Name:            "generic",
		ScopeName:       "io.opentelemetry.generic",
		SeverityWeights: DefaultSeverityWeights(),
		Messages: []MessageTemplate{
			{
				Severity: plog.SeverityNumberInfo,
				Format:   "log message from %s at %s",
				Args:     []ArgGenerator{Static(serviceName), Timestamp(tsLayout)},
			},
		},
	}
}
