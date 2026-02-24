package loggen

import (
	"math/rand"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"
)

// GetAppProfile returns the AppProfile for the given scenario path, or nil if unknown.
// rng is used for deterministic pool generation (e.g. ZipfianIP); pass nil to use a default seed.
func GetAppProfile(path string, rng *rand.Rand) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	path = strings.TrimPrefix(path, "builtin/")
	switch path {
	case "k8s-nginx":
		return NginxProfile(rng)
	case "k8s-mysql":
		return MySQLProfile(rng)
	case "k8s-redis":
		return RedisProfile(rng)
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
