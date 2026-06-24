package s3

import (
	"context"
	"io"
	"sort"
	"time"

	domain_search "github.com/EliLillyCo/work-dashboard/internal/domain/search"
	"github.com/EliLillyCo/work-dashboard/internal/infra/awsclient"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Repository struct {
	client *awsclient.AWSClient
}

func NewRepository(client *awsclient.AWSClient) *Repository {
	return &Repository{client: client}
}

func (r *Repository) HeadObjectSize(ctx context.Context, bucket, key string) (int64, error) {
	out, err := r.client.S3.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return 0, err
	}
	if out.ContentLength == nil {
		return 0, nil
	}
	return *out.ContentLength, nil
}

func (r *Repository) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := r.client.S3.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (r *Repository) FindEmail(ctx context.Context, bucket, key, email string) (*domain_search.RecordMatch, error) {
	match, err := r.client.FindEmailInS3JSON(ctx, bucket, key, email)
	if err != nil || match == nil {
		return nil, err
	}
	return &domain_search.RecordMatch{Index: match.Index, Record: match.Record}, nil
}

func (r *Repository) FindString(ctx context.Context, bucket, key, query string, limit int) ([]domain_search.StringMatch, error) {
	matches, err := r.client.FindStringInS3JSON(ctx, bucket, key, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]domain_search.StringMatch, 0, len(matches))
	for _, match := range matches {
		out = append(out, domain_search.StringMatch{
			Index:  match.Index,
			Record: match.Record,
		})
	}
	return out, nil
}

func (r *Repository) ListObjects(ctx context.Context, bucket, prefix string, maxKeys int32) ([]domain_search.ObjectInfo, error) {
	out, err := r.client.S3.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket:  &bucket,
		Prefix:  &prefix,
		MaxKeys: &maxKeys,
	})
	if err != nil {
		return nil, err
	}

	items := make([]domain_search.ObjectInfo, 0, len(out.Contents))
	for _, item := range out.Contents {
		if item.Key == nil {
			continue
		}
		items = append(items, domain_search.ObjectInfo{
			Key:          *item.Key,
			SizeBytes:    derefInt64(item.Size),
			LastModified: derefTime(item.LastModified),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].LastModified.After(items[j].LastModified)
	})

	return items, nil
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefTime(v *time.Time) time.Time {
	if v == nil {
		return time.Time{}
	}
	return *v
}
