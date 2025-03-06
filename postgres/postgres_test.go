package postgres_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/G-Research/otel-partial-connector/postgres"
	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

var (
	protoMarshaller   ptrace.ProtoMarshaler
	protoUnmarshaller ptrace.ProtoUnmarshaler
)

type TestSuite struct {
	suite.Suite
	tp *TestPostgres
}

func TestIntegration(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func (hs *TestSuite) SetupSuite() {
	ctx := context.Background()

	cfg := InstanceConfig{
		User:     "postgres",
		Password: "test",
		DBName:   "test",
		Host:     "localhost",
		Port:     "25432",
	}

	t := hs.T()
	tp, err := NewTestPostgres(ctx, cfg)
	require.NoError(t, err, "failed to start postgres container")

	hs.tp = tp
	migrationDir, err := migrationDir()
	require.NoError(t, err, "failed to find migration directory")

	err = tp.Migration(ctx, migrationDir)
	require.NoError(t, err, "failed to run migration")

	hs.tp.db, err = postgres.NewDB(ctx, cfg.DBConnStr())
	require.NoError(t, err)
}

func (ts *TestSuite) TestCreateTraces() {
	ctx := context.Background()
	t := ts.T()
	traceID, spanID, trace := generateTrace(t)

	b, err := protoMarshaller.MarshalTraces(trace)
	require.NoError(t, err)

	err = ts.tp.db.PutTrace(context.Background(), traceID.String(), spanID.String(), b)
	require.NoError(t, err, "failed to put the first trace")

	err = ts.tp.db.PutTrace(context.Background(), traceID.String(), spanID.String(), b)
	require.NoError(t, err, "repeated put should succeed")

	rows, err := ts.tp.db.Query(ctx, "SELECT trace from partial_traces")
	require.NoError(t, err)
	defer rows.Close()

	var got []ptrace.Traces
	for rows.Next() {
		var bytes []byte
		err = rows.Scan(&bytes)
		require.NoError(t, err)

		trace, err := protoUnmarshaller.UnmarshalTraces(bytes)
		require.NoError(t, err)
		got = append(got, trace)
	}

	assert.Equal(t, 1, len(got))
	assert.Equal(t, trace, got[0])
}

func (tp *TestPostgres) Migration(ctx context.Context, dir string) error {
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}

	d := "file://" + absPath
	m, err := migrate.New(d, tp.cfg.MigrationConnStr())
	if err != nil {
		return fmt.Errorf("failed to create migration on conn %q: %v", tp.cfg.MigrationConnStr(), err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migration: %v", err)
	}

	return nil
}

func migrationDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %v", err)
	}

	// find go.mod file
	for wd != "/" {
		_, err := os.Stat(wd + "/go.mod")
		if err == nil {
			break
		}
		wd = filepath.Dir(wd)
	}

	if wd == "/" {
		return "", fmt.Errorf("go.mod file not found")
	}

	return filepath.Clean(path.Join(wd, "migrations")), nil
}

type InstanceConfig struct {
	User     string
	Password string
	DBName   string
	Host     string
	Port     string
}

func (c *InstanceConfig) DBConnStr() string {
	return fmt.Sprintf(
		"host='%s' port='%s' user='%s' dbname='%s' sslmode='%s' password='%s' pool_max_conns='%s'",
		c.Host, c.Port, c.User, c.DBName, "disable", c.Password, "5",
	)
}

func (c *InstanceConfig) MigrationConnStr() string {
	return fmt.Sprintf("pgx5://%s:%s@%s:%s/%s?sslmode=disable", c.User, c.Password, c.Host, c.Port, c.DBName)
}

type TestPostgres struct {
	container testcontainers.Container
	cfg       InstanceConfig
	db        *postgres.DB
}

func NewTestPostgres(ctx context.Context, cfg InstanceConfig) (*TestPostgres, error) {
	port := fmt.Sprintf("%s:5432/tcp", cfg.Port)
	cr := testcontainers.ContainerRequest{
		Image: "postgres:17-bookworm",
		Env: map[string]string{
			"POSTGRES_USER":     cfg.User,
			"POSTGRES_PASSWORD": cfg.Password,
			"POSTGRES_DB":       cfg.DBName,
		},
		ExposedPorts: []string{port},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(5 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: cr,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start postgres container: %v", err)
	}

	return &TestPostgres{
		container: container,
		cfg:       cfg,
	}, nil
}

func generateTrace(t *testing.T) (traceID pcommon.TraceID, spanID pcommon.SpanID, trace ptrace.Traces) {
	traces := ptrace.NewTraces()

	rs := traces.ResourceSpans().AppendEmpty()
	r := rs.Resource()
	attrs := r.Attributes()
	attrs.PutInt("test", 7)

	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName("example")
	ss.Scope().SetVersion("v1.0")
	s := ss.Spans().AppendEmpty()
	sattrs := s.Attributes()
	sattrs.PutBool("ok", true)

	traceID = newTraceID(t)
	spanID = newSpanID(t)

	s.SetTraceID(traceID)
	s.SetSpanID(spanID)

	return traceID, spanID, traces
}

func newTraceID(*testing.T) pcommon.TraceID {
	return pcommon.TraceID(uuid.New())
}

func newSpanID(t *testing.T) pcommon.SpanID {
	var sid [8]byte
	_, err := rand.Read(sid[:])
	require.NoError(t, err)
	spanID := pcommon.SpanID(sid)
	return spanID
}
