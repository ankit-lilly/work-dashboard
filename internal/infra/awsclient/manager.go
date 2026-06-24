package awsclient

import (
	"context"

	legacyaws "github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/EliLillyCo/work-dashboard/internal/config"
)

type Manager struct {
	Clients map[string]*AWSClient
}

func NewClientManager(ctx context.Context, cfg *config.Config) (*Manager, error) {
	legacyManager, err := legacyaws.NewClientManager(ctx, cfg)
	if err != nil {
		return nil, err
	}

	clients := make(map[string]*AWSClient, len(legacyManager.Clients))
	for env, client := range legacyManager.Clients {
		clients[env] = client
	}

	return &Manager{Clients: clients}, nil
}
