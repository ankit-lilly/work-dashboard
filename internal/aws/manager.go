package aws

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

type Client struct {
	EnvName string
	Config  aws.Config
	Sfn     *sfn.Client
	S3      *s3.Client
	Logs    *cloudwatchlogs.Client

	StateMachinesMu sync.RWMutex
	StateMachines   []types.StateMachineListItem
	StateMachinesAt time.Time
}

type ClientManager struct {
	Clients map[string]*Client
}

func NewClientManager(ctx context.Context, cfg *config.Config) (*ClientManager, error) {
	manager := &ClientManager{
		Clients: make(map[string]*Client),
	}

	for _, mapping := range cfg.Envs {
		fmt.Printf("Initializing AWS client for environment: %s (profile: %s, region: %s)\n", mapping.Name, mapping.Profile, mapping.Region)

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithSharedConfigProfile(mapping.Profile),
			awsconfig.WithRegion(mapping.Region),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config for %s: %w", mapping.Name, err)
		}

		client := &Client{
			EnvName: mapping.Name,
			Config:  awsCfg,
			Sfn:     sfn.NewFromConfig(awsCfg),
			S3:      s3.NewFromConfig(awsCfg),
			Logs:    cloudwatchlogs.NewFromConfig(awsCfg),
		}

		manager.Clients[mapping.Name] = client
	}

	return manager, nil
}
