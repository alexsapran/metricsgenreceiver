package loggen

import (
	"math/rand"

	"go.opentelemetry.io/collector/pdata/plog"
)

var mysqlDBNames = []string{"orders", "users", "products", "analytics", "auth", "main"}
var mysqlUsers = []string{"app_user", "replicator", "admin", "root", "migration"}
var mysqlTables = []string{"users", "orders", "products", "sessions", "audit_log"}
var mysqlColumns = []string{"id", "user_id", "email", "status", "created_at"}

func MySQLProfile(rng *rand.Rand, ipCfg *IPPoolConfig) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	zipfIP := ZipfianIP(5000, rng, ipCfg)
	crossCutting := ErrorMessageAttrs(rng, JavaStackTrace(220, 1500, rng))
	longTail := LongTailAttrs(rng)
	msgs := append(
		append(mysqlInfoLogs(zipfIP), mysqlDebugLogs(rng)...),
		mysqlWarnLogs(zipfIP, rng)...,
	)
	return &AppProfile{
		Name:            "mysql",
		ScopeName:       "io.opentelemetry.mysql",
		SeverityWeights: DefaultSeverityWeights(),
		Messages:        appendCrossCutting(msgs, crossCutting),
		LongTail:        longTail,
	}
}

func mysqlInfoLogs(zipfIP ArgGenerator) []MessageTemplate {
	tsLayout := "2006-01-02 15:04:05.000000"
	threadID := RandomInt(1, 100)
	connID := RandomInt(1000, 99999)
	code := RandomFrom("010901", "010907", "010909", "010912")
	db := RandomPath(mysqlDBNames)
	user := RandomPath(mysqlUsers)
	hostname := RandomFrom("mysql-primary-0", "mysql-replica-1", "mysql-2")
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s %d [Note] [MY-%s] [Server] %s: ready for connections. Version: '8.0.36' socket: '/var/run/mysqld/mysqld.sock' port: 3306",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, hostname},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s %d [Note] [MY-%s] [Server] %s: ready",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, hostname},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s %d [Note] [MY-%s] [InnoDB] Buffer pool(s) load completed at %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s %d [Note] [MY-%s] [Server] Aborted connection %d to db: '%s' user: '%s' host: '%s' (Got timeout reading communication packets)",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, connID, db, user, zipfIP},
			AttrFromArg: map[string]int{"db.name": 4},
			Attrs:       []AttrGen{{"db.system", Static("mysql")}, {"db.user", user}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s %d [Note] [MY-%s] [Repl] Replica SQL thread for channel '' started, Replica has read all relay log; waiting for more updates",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
	}
}

func mysqlDebugLogs(rng *rand.Rand) []MessageTemplate {
	tsLayout := "2006-01-02 15:04:05.000000"
	threadID := RandomInt(1, 100)
	code := RandomFrom("010901", "010907")
	db := RandomPath(mysqlDBNames)
	table := RandomPath(mysqlTables)
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberDebug,
			Format:   "%s %d [Note] [MY-%s] [InnoDB] page_cleaner: flushed %d pages, %d%% of innodb_io_capacity",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, RandomInt(50, 500), RandomInt(10, 100)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity:     plog.SeverityNumberDebug,
			Format:       "%s %d [Note] [MY-%s] [Server] Analyzing table '%s.%s': status OK",
			Args:         []ArgGenerator{Timestamp(tsLayout), threadID, code, db, table},
			AttrFromArg:  map[string]int{"db.name": 3, "db.sql.table": 4},
			Attrs:        []AttrGen{{"db.system", Static("mysql")}, {"db.operation.name", Static("ANALYZE")}},
		},
		{
			Severity:     plog.SeverityNumberDebug,
			Format:       "%s %d [Note] [MY-%s] [Server] EXPLAIN for query on %s.%s:\n%s",
			Args:         []ArgGenerator{Timestamp(tsLayout), threadID, code, db, table, SQLExplainPlan(600, 2500, rng)},
			AttrFromArg:  map[string]int{"db.name": 3, "db.sql.table": 4},
			Attrs:        []AttrGen{{"db.system", Static("mysql")}, {"db.operation.name", Static("SELECT")}},
		},
	}
}

func mysqlWarnLogs(zipfIP ArgGenerator, rng *rand.Rand) []MessageTemplate {
	tsLayout := "2006-01-02 15:04:05.000000"
	threadID := RandomInt(1, 100)
	connID := RandomInt(1000, 99999)
	code := RandomFrom("010907", "010911", "010914")
	db := RandomPath(mysqlDBNames)
	user := RandomPath(mysqlUsers)
	osThreadID := RandomInt(1000, 99999)
	table := RandomPath(mysqlTables)
	column := RandomPath(mysqlColumns)
	duration := RandomInt(120, 300)
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s %d [Warning] [MY-%s] [Server] Aborted connection %d to db: '%s' user: '%s' host: '%s' (Got an error reading communication packets)",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, connID, db, user, zipfIP},
			AttrFromArg: map[string]int{"db.name": 4},
			Attrs:       []AttrGen{{"db.system", Static("mysql")}, {"db.user", user}},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s %d [Warning] [MY-%s] [InnoDB] A long semaphore wait (>120 seconds, %ds) for thread %d",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, duration, osThreadID},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity:     plog.SeverityNumberWarn,
			Format:       "%s %d [Warning] [MY-%s] [Server] Slow query detected: %ds, rows_examined: %d, db: '%s', query: 'SELECT * FROM %s WHERE %s = %%s'",
			Args:         []ArgGenerator{Timestamp(tsLayout), threadID, code, RandomInt(2, 30), RandomInt(1000, 100000), db, table, column},
			AttrFromArg:  map[string]int{"db.name": 6, "db.sql.table": 7},
			Attrs:        []AttrGen{{"db.system", Static("mysql")}, {"db.operation.name", Static("SELECT")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s %d [ERROR] [MY-%s] [Server] Can't start server: Bind on TCP/IP port: Address already in use",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s %d [ERROR] [MY-%s] [InnoDB] Cannot allocate memory for the buffer pool",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s %d [ERROR] [MY-%s] [Server] Got error %d from storage engine",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, RandomInt(1030, 1035)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity:     plog.SeverityNumberError,
			Format:       "%s %d [ERROR] [MY-%s] [Repl] Error 'Table '%s.%s' doesn't exist' on query.",
			Args:         []ArgGenerator{Timestamp(tsLayout), threadID, code, db, table},
			AttrFromArg:  map[string]int{"db.name": 3, "db.sql.table": 4},
			Attrs:        []AttrGen{{"db.system", Static("mysql")}, {"db.operation.name", Static("SELECT")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s %d [ERROR] [MY-%s] [InnoDB] LATEST DETECTED DEADLOCK\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, MySQLCrashTrace(500, 2500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s %d [ERROR] [MY-%s] [InnoDB] Cannot allocate memory for the buffer pool\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, MySQLCrashTrace(600, 3000, rng)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s %d [ERROR] [MY-%s] [InnoDB] LATEST DETECTED DEADLOCK",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s %d [ERROR] [MY-%s] [Server] Out of memory; check if mysqld or some other process uses all available memory",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s %d [ERROR] [MY-%s] [InnoDB] LATEST DETECTED DEADLOCK\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, MySQLCrashTrace(800, 4500, rng)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s %d [ERROR] [MY-%s] [Server] Out of memory\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, MySQLCrashTrace(1000, 5000, rng)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s %d [ERROR] [MY-%s] [InnoDB] Assertion failure in thread %d\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), threadID, code, osThreadID, MySQLCrashTrace(700, 4000, rng)},
			Attrs:    []AttrGen{{"db.system", Static("mysql")}},
		},
	}
}
