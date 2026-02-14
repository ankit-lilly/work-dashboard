package aws

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/pi"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"golang.org/x/time/rate"
)

type Client struct {
	EnvName string
	Config  aws.Config
	Sfn     *sfn.Client
	S3      *s3.Client
	Logs    *cloudwatchlogs.Client
	RDS     *rds.Client
	PI      *pi.Client
	CW      *cloudwatch.Client

	StateMachinesMu sync.RWMutex
	StateMachines   []types.StateMachineListItem
	StateMachinesAt time.Time

	execCacheMu sync.Mutex
	execCache   map[execCacheKey]execCacheEntry
	execLimiter *rate.Limiter
}

type ClientManager struct {
	Clients map[string]*Client
}

/*

@docs

NewClientManager initializes AWS clients for each environment defined in the configuration.

This works by iterating over the environment mappings specified in the configuration file
( env:profile:region format) and creating a new AWS configuration. This works by looking for aws
sso profiles in the user's AWS config file (typically located at ~/.aws/config) and loading the
appropriate credentials and region settings for each environment.( whatever is specified in the
config file for that profile)

*/

func NewClientManager(ctx context.Context, cfg *config.Config) (*ClientManager, error) {
	manager := &ClientManager{
		Clients: make(map[string]*Client),
	}

	for _, mapping := range cfg.Envs {
		slog.Info("initializing aws client", "env", mapping.Name, "profile", mapping.Profile, "region", mapping.Region)

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithSharedConfigProfile(mapping.Profile),
			awsconfig.WithRegion(mapping.Region),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config for %s: %w", mapping.Name, err)
		}

		client := &Client{
			EnvName:     mapping.Name,
			Config:      awsCfg,
			Sfn:         sfn.NewFromConfig(awsCfg),
			S3:          s3.NewFromConfig(awsCfg),
			Logs:        cloudwatchlogs.NewFromConfig(awsCfg),
			RDS:         rds.NewFromConfig(awsCfg),
			PI:          pi.NewFromConfig(awsCfg),
			CW:          cloudwatch.NewFromConfig(awsCfg),
			execCache:   make(map[execCacheKey]execCacheEntry),
			execLimiter: rate.NewLimiter(rate.Every(200*time.Millisecond), 5),
		}

		manager.Clients[mapping.Name] = client
	}

	return manager, nil
}

// IsCredentialError checks if an error is due to expired or invalid AWS credentials
func IsCredentialError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "expired") ||
		strings.Contains(errStr, "invalid token") ||
		strings.Contains(errStr, "token is expired") ||
		strings.Contains(errStr, "security token included in the request is expired") ||
		strings.Contains(errStr, "expiredtoken") ||
		strings.Contains(errStr, "expiredtokenexception") ||
		strings.Contains(errStr, "credentials") && strings.Contains(errStr, "expired") ||
		strings.Contains(errStr, "failed to refresh cached credentials") ||
		strings.Contains(errStr, "refresh cached sso token failed") ||
		strings.Contains(errStr, "unable to refresh sso token")
}
