package loggen

import (
	"math/rand"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"
)

// GetAppProfile returns the AppProfile for the given scenario path, or nil if unknown.
// rng is used for deterministic pool generation (e.g. ZipfianIP); pass nil to use a default seed.
// ipCfg configures the IP pool (CIDRs, skew); pass nil for defaults.
// scale is the number of pod instances, used to auto-size the IP pool.
func GetAppProfile(path string, rng *rand.Rand, ipCfg *IPPoolConfig, scale int) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	poolSize := scale * 10
	if poolSize < 500 {
		poolSize = 500
	}
	if ipCfg != nil {
		ipCfg.PoolSize = poolSize
	} else {
		ipCfg = &IPPoolConfig{PoolSize: poolSize}
	}
	path = strings.TrimPrefix(path, "builtin/")
	switch path {
	case "k8s-nginx":
		return NginxProfile(rng, ipCfg)
	case "k8s-mysql":
		return MySQLProfile(rng, ipCfg)
	case "k8s-redis":
		return RedisProfile(rng, ipCfg)
	case "k8s-goapp":
		return GoAppProfile(rng)
	case "k8s-proxy":
		return ProxyProfile(rng, ipCfg)
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
