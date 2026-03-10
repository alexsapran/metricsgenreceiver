package loggen

import (
	"math/rand"
	"strconv"
	"strings"
)

var goStackPackages = []string{
	"main", "net/http", "runtime", "encoding/json", "database/sql",
	"github.com/gin-gonic/gin", "go.opentelemetry.io/otel", "internal/handler",
	"server", "db", "cache", "worker", "grpc/client",
}
var goStackFiles = []string{
	"handler.go", "server.go", "main.go", "query.go", "connection.go",
	"client.go", "processor.go", "middleware.go", "router.go", "context.go",
}
var goStackFuncs = []string{
	"(*Handler).ServeHTTP", "(*Server).ListenAndServe", "main.main",
	"(*DB).Query", "(*Conn).Exec", "(*Client).Call", "(*Processor).Run",
	"(*Middleware).Handle", "(*Router).ServeHTTP", "(*Context).Next",
}

const tracePoolSize = 100

// buildGoStackTrace generates a single Go stack trace string of the given target length.
func buildGoStackTrace(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	b.WriteString("goroutine ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" [running]:\n")
	frames := 8 + r.Intn(80)
	for i := 0; i < frames && b.Len() < targetLen; i++ {
		pkg := goStackPackages[r.Intn(len(goStackPackages))]
		b.WriteString(pkg)
		b.WriteByte('.')
		b.WriteString(goStackFuncs[r.Intn(len(goStackFuncs))])
		b.WriteString("(0x")
		b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
		b.WriteString(")\n\t/app/")
		b.WriteString(pkg)
		b.WriteByte('/')
		b.WriteString(goStackFiles[r.Intn(len(goStackFiles))])
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(r.Intn(200) + 1))
		b.WriteString(" +0x")
		b.WriteString(strconv.FormatInt(int64(r.Intn(0x2000)+0x100), 16))
		b.WriteByte('\n')
	}
	return b.String()
}

// GoStackTrace pre-generates a pool of Go stack traces and returns an ArgGenerator
// that picks from the pool at runtime (zero allocation in the hot path).
func GoStackTrace(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildGoStackTrace(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

var javaStackPackages = []string{
	"com.example.service", "com.example.controller", "org.springframework",
	"java.util", "java.lang", "io.netty", "org.hibernate", "com.fasterxml.jackson",
}
var javaStackClasses = []string{
	"UserService", "OrderController", "RestTemplate", "HttpClient",
	"TransactionManager", "EntityManager", "ObjectMapper", "HandlerAdapter",
}
var javaStackMethods = []string{
	"getUser", "handle", "execute", "process", "invoke", "doFilter",
	"findById", "save", "serialize", "deserialize",
}
var javaStackFiles = []string{
	"userservice", "ordercontroller", "resttemplate", "httpclient",
	"transactionmanager", "entitymanager", "objectmapper", "handleradapter",
}

func buildJavaStackTrace(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	exceptions := [...]string{"RuntimeException", "NullPointerException", "IOException", "SQLException"}
	msgs := [...]string{"Connection timeout", "null pointer", "connection refused", "deadline exceeded"}
	b.WriteString(exceptions[r.Intn(len(exceptions))])
	b.WriteString(": ")
	b.WriteString(msgs[r.Intn(len(msgs))])
	b.WriteByte('\n')
	frames := 10 + r.Intn(80)
	for i := 0; i < frames && b.Len() < targetLen; i++ {
		b.WriteString("\tat ")
		b.WriteString(javaStackPackages[r.Intn(len(javaStackPackages))])
		b.WriteByte('.')
		b.WriteString(javaStackClasses[r.Intn(len(javaStackClasses))])
		b.WriteByte('.')
		b.WriteString(javaStackMethods[r.Intn(len(javaStackMethods))])
		b.WriteByte('(')
		b.WriteString(javaStackFiles[r.Intn(len(javaStackFiles))])
		b.WriteString(".java:")
		b.WriteString(strconv.Itoa(r.Intn(200) + 1))
		b.WriteString(")\n")
	}
	return b.String()
}

// JavaStackTrace pre-generates a pool of Java-like stack traces.
func JavaStackTrace(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildJavaStackTrace(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildMySQLCrashTrace(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	trx1, trx2 := r.Intn(90000)+10000, r.Intn(90000)+10000
	table := mysqlTables[r.Intn(len(mysqlTables))]
	b.WriteString("*** (1) TRANSACTION:\nTRANSACTION ")
	b.WriteString(strconv.Itoa(trx1))
	b.WriteString(", ACTIVE ")
	b.WriteString(strconv.Itoa(r.Intn(30) + 1))
	b.WriteString(" sec starting index read\nmysql tables in use 1, locked 1\nLOCK WAIT ")
	b.WriteString(strconv.Itoa(r.Intn(5) + 1))
	b.WriteString(" lock struct(s), heap size ")
	b.WriteString(strconv.Itoa(r.Intn(2000) + 500))
	b.WriteString(", ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" row lock(s)\nMySQL thread id ")
	b.WriteString(strconv.Itoa(r.Intn(99999) + 1))
	b.WriteString(", OS thread handle 0x")
	b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
	b.WriteString(", query id ")
	b.WriteString(strconv.Itoa(r.Intn(999999) + 1))
	b.WriteString("\nUPDATE ")
	b.WriteString(table)
	b.WriteString(" SET status = %s WHERE id = %s\n*** (2) WAITING FOR THIS LOCK TO BE GRANTED:\nRECORD LOCKS space id ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" page no ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" n bits ")
	b.WriteString(strconv.Itoa(r.Intn(64) + 8))
	b.WriteString(" index PRIMARY of table `")
	b.WriteString(mysqlDBNames[r.Intn(len(mysqlDBNames))])
	b.WriteString("`.`")
	b.WriteString(table)
	b.WriteString("`\n*** (2) TRANSACTION:\nTRANSACTION ")
	b.WriteString(strconv.Itoa(trx2))
	b.WriteString(", ACTIVE ")
	b.WriteString(strconv.Itoa(r.Intn(20) + 1))
	b.WriteString(" sec fetching rows\n*** WE ROLL BACK TRANSACTION (1)\n")
	for b.Len() < targetLen {
		b.WriteString("---TRANSACTION ")
		b.WriteString(strconv.Itoa(r.Intn(90000) + 10000))
		b.WriteString(", ACTIVE ")
		b.WriteString(strconv.Itoa(r.Intn(10)))
		b.WriteString(" sec\nmysql tables in use 1, locked 1\n")
		b.WriteString(strconv.Itoa(r.Intn(3) + 1))
		b.WriteString(" lock struct(s)\n")
	}
	return b.String()
}

// MySQLCrashTrace pre-generates a pool of InnoDB deadlock/crash traces.
func MySQLCrashTrace(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildMySQLCrashTrace(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

var nginxErrorMsgs = [...]string{
	"upstream prematurely closed connection while reading response header",
	"recv() failed (104: Connection reset by peer) while reading upstream",
	"send() failed (111: Connection refused) while sending to upstream",
	"SSL_do_handshake() failed (SSL: error:0A0C0103:SSL routines::internal error)",
	"open() \"/var/cache/nginx/proxy/7/00/0000000007\" failed (13: Permission denied)",
}

func buildNginxErrorDetails(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	pid := strconv.Itoa(r.Intn(99999) + 1)
	tid := strconv.Itoa(r.Intn(2))
	connBase := r.Intn(89999) + 10000
	for b.Len() < targetLen {
		b.WriteString("2024/01/15 12:00:00 [error] ")
		b.WriteString(pid)
		b.WriteByte('#')
		b.WriteString(tid)
		b.WriteString(": *")
		b.WriteString(strconv.Itoa(connBase + r.Intn(100)))
		b.WriteByte(' ')
		b.WriteString(nginxErrorMsgs[r.Intn(len(nginxErrorMsgs))])
		b.WriteByte('\n')
	}
	return b.String()
}

// NginxErrorDetails pre-generates a pool of multi-line nginx error output.
func NginxErrorDetails(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildNginxErrorDetails(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildLargeJSONPayload(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 256)
	b.WriteString(`{"items":[`)
	id := make([]byte, 8)
	for b.Len() < targetLen {
		if b.Len() > 20 {
			b.WriteByte(',')
		}
		for i := range id {
			id[i] = hexChars[r.Intn(16)]
		}
		b.WriteString(`{"id":"`)
		b.Write(id)
		b.WriteString(`","name":"product-`)
		b.WriteString(strconv.Itoa(r.Intn(10000)))
		b.WriteString(`","price":`)
		b.WriteString(strconv.Itoa(r.Intn(500) + 10))
		b.WriteByte('}')
	}
	b.WriteString("]}")
	return b.String()
}

// LargeJSONPayload pre-generates a pool of large JSON request/response bodies.
func LargeJSONPayload(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildLargeJSONPayload(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildSQLExplainPlan(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 256)
	table := mysqlTables[r.Intn(len(mysqlTables))]
	b.WriteString("EXPLAIN SELECT * FROM ")
	b.WriteString(table)
	b.WriteString(" WHERE id = %s\n+----+-------------+-------+------+---------------+------+---------+------+------+----------+\n| id | select_type | table | type | possible_keys | key  | key_len | ref  | rows | Extra    |\n+----+-------------+-------+------+---------------+------+---------+------+------+----------+\n")
	for b.Len() < targetLen {
		b.WriteString("| ")
		b.WriteString(strconv.Itoa(r.Intn(5) + 1))
		b.WriteString(" | SIMPLE       | ")
		b.WriteString(table)
		b.WriteString("   | ALL  | NULL          | NULL | NULL    | NULL | ")
		b.WriteString(strconv.Itoa(r.Intn(100000) + 100))
		b.WriteString("   |          |\n")
	}
	b.WriteString("+----+-------------+-------+------+---------------+------+---------+------+------+----------+\n")
	return b.String()
}

// SQLExplainPlan pre-generates a pool of MySQL EXPLAIN output.
func SQLExplainPlan(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildSQLExplainPlan(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildRedisSlowlogOutput(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 256)
	cmds := [...]string{"GET", "SET", "HGETALL", "LRANGE", "SMEMBERS", "ZRANGE"}
	id := make([]byte, 12)
	for b.Len() < targetLen {
		b.WriteString(strconv.Itoa(r.Intn(100) + 1))
		b.WriteString(") 1) (integer) ")
		b.WriteString(strconv.Itoa(r.Intn(999)))
		b.WriteString("\n   2) (integer) ")
		b.WriteString(strconv.Itoa(r.Intn(999999)))
		b.WriteString("\n   3) (integer) ")
		b.WriteString(strconv.Itoa(r.Intn(50000)))
		b.WriteString("\n   4) 1) \"")
		b.WriteString(cmds[r.Intn(len(cmds))])
		b.WriteString("\"\n      2) \"")
		for i := range id {
			id[i] = hexChars[r.Intn(16)]
		}
		b.Write(id)
		b.WriteString("\"\n")
	}
	return b.String()
}

// RedisSlowlogOutput pre-generates a pool of Redis SLOWLOG output.
func RedisSlowlogOutput(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildRedisSlowlogOutput(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildRedisCrashReport(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	pid := r.Intn(99999) + 1
	b.WriteString("=== REDIS BUG REPORT START: Cut & paste starting from here ===\nRedis version: ")
	b.WriteString(redisVersions[r.Intn(len(redisVersions))])
	b.WriteString("\nRedis pid:")
	b.WriteString(strconv.Itoa(pid))
	b.WriteString("\nOS:Linux 5.15.0 x86_64\nUptime: 0.0 sec\nFatal signal: 11 (SIGSEGV) at 0x")
	b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
	b.WriteString(", pid ")
	b.WriteString(strconv.Itoa(pid))
	b.WriteString(", tid ")
	b.WriteString(strconv.Itoa(r.Intn(99999) + 1))
	b.WriteString("\nBacktrace:\n")
	frames := 15 + r.Intn(25)
	for i := 0; i < frames && b.Len() < targetLen; i++ {
		b.WriteByte('#')
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" 0x")
		b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
		b.WriteString(" in ?? () from /usr/lib/redis/redis-server\n")
	}
	b.WriteString("=== REDIS BUG REPORT END. PLEASE INCLUDE EVERYTHING ABOVE ===\n")
	for b.Len() < targetLen {
		b.WriteString("Thread ")
		b.WriteString(strconv.Itoa(r.Intn(20)))
		b.WriteString(": 0x")
		b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
		b.WriteByte('\n')
	}
	return b.String()
}

// RedisCrashReport pre-generates a pool of Redis crash report output.
func RedisCrashReport(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildRedisCrashReport(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}
