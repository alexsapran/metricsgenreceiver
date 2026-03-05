package loggen

import (
	"math/rand"

	"go.opentelemetry.io/collector/pdata/plog"
)

var redisVersions = []string{"7.2.4", "7.0.12", "6.2.6"}
var redisEvictionPolicies = []string{"allkeys-lru", "volatile-lru", "noeviction"}

func RedisProfile(rng *rand.Rand, ipCfg *IPPoolConfig) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	zipfIP := ZipfianIP(5000, rng, ipCfg)
	crossCutting := ErrorMessageAttrs(rng, GoStackTrace(800, 8500, rng))
	longTail := LongTailAttrs(rng)
	msgs := append(
		append(redisInfoLogs(zipfIP), redisDebugLogs(zipfIP, rng)...),
		redisWarnLogs(rng)...,
	)
	return &AppProfile{
		Name:            "redis",
		ScopeName:       "io.opentelemetry.redis",
		SeverityWeights: DefaultSeverityWeights(),
		Messages:        appendCrossCutting(msgs, crossCutting),
		LongTail:        longTail,
	}
}

func redisInfoLogs(zipfIP ArgGenerator) []MessageTemplate {
	tsLayout := "02 Jan 2006 15:04:05.000"
	pid := RandomInt(1, 99999)
	role := RandomFrom("M", "S", "C")
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s + pong",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s # Server started, Redis version=%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomPath(redisVersions)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s * Ready to accept connections tcp",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s * DB loaded from append only file: %d seconds",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(1, 15)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s * %d clients connected (%d replicas), %d bytes in use",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(1, 100), RandomInt(0, 5), RandomInt(1000000, 50000000)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s * Background saving started by pid %d",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(1000, 99999)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s * Background saving terminated with success",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%d:%s %s # Connection accepted from %s:%d",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), zipfIP, RandomInt(40000, 65000)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}, {"net.peer.port", RandomFromInt(6379)}},
		},
	}
}

func redisDebugLogs(zipfIP ArgGenerator, rng *rand.Rand) []MessageTemplate {
	tsLayout := "02 Jan 2006 15:04:05.000"
	pid := RandomInt(1, 99999)
	role := RandomFrom("M", "S", "C")
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberDebug,
			Format:   "%d:%s %s - Accepted %s:%d -> %s:%d",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), zipfIP, RandomInt(40000, 65000), RandomFrom("127.0.0.1", "10.0.0.1"), RandomInt(6379, 6381)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}, {"net.peer.port", RandomFromInt(6379)}},
		},
		{
			Severity: plog.SeverityNumberDebug,
			Format:   "%d:%s %s - 0 clients connected (0 replicas), %d bytes in use",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(100000, 5000000)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberDebug,
			Format:   "%d:%s %s * SLOWLOG output:\n%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RedisSlowlogOutput(600, 2500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}, {"db.operation.name", RandomFrom("GET", "SET", "HGETALL", "LRANGE")}},
		},
	}
}

func redisWarnLogs(rng *rand.Rand) []MessageTemplate {
	tsLayout := "02 Jan 2006 15:04:05.000"
	pid := RandomInt(1, 99999)
	role := RandomFrom("M", "S")
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%d:%s %s # WARNING: %d clients found in the connected clients list with idle time >= %d seconds, disconnecting them.",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(1, 10), RandomInt(300, 3600)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%d:%s %s # WARNING overcommit_memory is set to 0! Background save may fail under low memory condition.",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%d:%s %s * Reaching maxmemory limit (%d bytes), evicting keys using %s policy",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(100000000, 8000000000), RandomPath(redisEvictionPolicies)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%d:%s %s # Error accepting a client connection: %s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomFrom("Connection reset by peer", "Invalid argument", "Too many open files")},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%d:%s %s # Error opening /setting AOF rewrite IPC pipes: %s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomFrom("Broken pipe", "Invalid argument", "No such file or directory")},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%d:%s %s # MISCONF Redis is configured to save RDB snapshots, but it's currently unable to persist to disk.",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%d:%s %s # Can't save in background: fork: Cannot allocate memory",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%d:%s %s # Error accepting a client connection: %s\n%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomFrom("Connection reset by peer", "Invalid argument", "Too many open files"), RedisCrashReport(1500, 8000, rng)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%d:%s %s # Can't save in background: fork: Cannot allocate memory\n%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RedisCrashReport(2000, 8500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%d:%s %s # Fatal error, can't open config file '%s'",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomFrom("/etc/redis/redis.conf", "/usr/local/etc/redis.conf")},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%d:%s %s # === REDIS BUG REPORT START: Cut & paste starting from here ===",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%d:%s %s # Fatal error, can't open config file '%s'\n%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomFrom("/etc/redis/redis.conf", "/usr/local/etc/redis.conf"), RedisCrashReport(2000, 8500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%d:%s %s # === REDIS BUG REPORT START: Cut & paste starting from here ===\n%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RedisCrashReport(2500, 8500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%d:%s %s # Fatal signal 11 (SIGSEGV) at 0x%x\n%s",
			Args:     []ArgGenerator{pid, role, Timestamp(tsLayout), RandomInt(0, 0xffffffff), RedisCrashReport(2000, 8500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("redis")}},
		},
	}
}
