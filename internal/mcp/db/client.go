package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool struct {
	mu    sync.Mutex
	pools map[string]*pgxpool.Pool // key: env name (e.g. "prod")
	cfg   Config
}

type Config struct {
	SecretIDTemplate string
	Envs             map[string]EnvInfo
}

type EnvInfo struct {
	Profile string
	Region  string
}

type dbSecret struct {
	Host     string `json:"host"`
	DBName   string `json:"dbname"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func NewPool(cfg Config) *Pool {
	return &Pool{
		pools: make(map[string]*pgxpool.Pool),
		cfg:   cfg,
	}
}

// Get returns (or lazily creates) a connection pool for the given environment.
func (p *Pool) Get(ctx context.Context, env string) (*pgxpool.Pool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if pool, ok := p.pools[env]; ok {
		return pool, nil
	}

	info, ok := p.cfg.Envs[env]
	if !ok {
		return nil, fmt.Errorf("unknown environment %q; available: %v", env, p.availableEnvs())
	}

	secretID := fmt.Sprintf(p.cfg.SecretIDTemplate, env)
	secret, err := fetchSecret(ctx, secretID, info.Profile, info.Region)
	if err != nil {
		return nil, fmt.Errorf("fetch DB secret for env=%s: %w", env, err)
	}

	dsn := fmt.Sprintf(
		"host=%s port=5432 dbname=%s user=%s password=%s sslmode=require",
		secret.Host, secret.DBName, secret.Username, secret.Password,
	)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN for env=%s: %w", env, err)
	}
	if os.Getenv("MCP_LOG_QUERIES") != "" {
		poolCfg.ConnConfig.Tracer = &queryLogger{}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to DB for env=%s: %w", env, err)
	}

	p.pools[env] = pool
	return pool, nil
}

// Close shuts down all pools.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pool := range p.pools {
		pool.Close()
	}
}

func (p *Pool) availableEnvs() []string {
	envs := make([]string, 0, len(p.cfg.Envs))
	for k := range p.cfg.Envs {
		envs = append(envs, k)
	}
	return envs
}

// ConfigSnapshot returns the list of configured environments for the list_environments tool.
func (p *Pool) ConfigSnapshot() map[string]any {
	envs := make([]map[string]string, 0, len(p.cfg.Envs))
	for name, info := range p.cfg.Envs {
		envs = append(envs, map[string]string{
			"env":     name,
			"profile": info.Profile,
			"region":  info.Region,
		})
	}
	return map[string]any{
		"environments":       envs,
		"secret_id_template": p.cfg.SecretIDTemplate,
	}
}

func fetchSecret(ctx context.Context, secretID, profile, region string) (*dbSecret, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(profile),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := secretsmanager.NewFromConfig(awsCfg)
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	})
	if err != nil {
		return nil, fmt.Errorf("GetSecretValue(%s): %w", secretID, err)
	}

	var s dbSecret
	if err := json.Unmarshal([]byte(aws.ToString(out.SecretString)), &s); err != nil {
		return nil, fmt.Errorf("parse secret JSON: %w", err)
	}
	return &s, nil
}

// queryLogger implements pgx.QueryTracer. Enabled via MCP_LOG_QUERIES env var.
type queryLogger struct{}

func (q *queryLogger) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	slog.Info("query", "sql", data.SQL, "args", data.Args)
	return ctx
}

func (q *queryLogger) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		slog.Error("query failed", "err", data.Err)
	}
}
