package lambda

import (
	"context"

	domain_lambda "github.com/EliLillyCo/work-dashboard/internal/domain/lambda"
	"github.com/EliLillyCo/work-dashboard/internal/infra/awsclient"
)

type Repository struct {
	client *awsclient.AWSClient
}

func NewRepository(client *awsclient.AWSClient) *Repository {
	return &Repository{client: client}
}

func (r *Repository) GetMetrics(ctx context.Context, functionName string, usedBy []string) (*domain_lambda.LambdaMetrics, error) {
	metrics, err := r.client.GetLambdaMetrics(ctx, functionName, usedBy)
	if err != nil {
		return nil, err
	}
	if metrics == nil {
		return nil, nil
	}

	return &domain_lambda.LambdaMetrics{
		Env:             metrics.Env,
		FunctionName:    metrics.FunctionName,
		FunctionArn:     metrics.FunctionArn,
		Timeout:         metrics.Timeout,
		RecentDuration:  metrics.RecentDuration,
		AvgDuration:     metrics.AvgDuration,
		MaxDuration:     metrics.MaxDuration,
		TimeoutPercent:  metrics.TimeoutPercent,
		MemoryAllocated: metrics.MemoryAllocated,
		MemoryUsed:      metrics.MemoryUsed,
		MemoryMax:       metrics.MemoryMax,
		MemoryRecent:    metrics.MemoryRecent,
		MemoryPercent:   metrics.MemoryPercent,
		InvocationCount: metrics.InvocationCount,
		TimeoutCount:    metrics.TimeoutCount,
		ErrorCount:      metrics.ErrorCount,
		UsedByStepFuncs: append([]string(nil), metrics.UsedByStepFuncs...),
		LastInvoked:     metrics.LastInvoked,
		FirstInvoked:    metrics.FirstInvoked,
		ConfigError:     metrics.ConfigError,
	}, nil
}
