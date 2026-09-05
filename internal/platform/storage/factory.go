package storage

import (
	"context"
	"errors"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/xgtian-root/aginex/internal/config"
)

func FromConfig(
	ctx context.Context,
	cfg config.Storage,
	publicURL string,
	policies ...FilePolicy,
) (Storage, error) {
	policy := DefaultFilePolicy()
	if len(policies) > 1 {
		return nil, errors.New("storage accepts at most one file policy")
	}
	if len(policies) == 1 {
		policy = policies[0]
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Driver {
	case "local":
		return NewLocal(
			cfg.LocalRoot,
			publicURL+"/api/v1/files/local-upload",
			publicURL+"/api/v1/files/local-content",
			policy,
		)
	case "s3":
		if cfg.Bucket == "" || cfg.Region == "" {
			return nil, errors.New("S3 storage requires bucket and region")
		}
		options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
		if cfg.AccessKeyID != "" {
			options = append(options, awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret, ""),
			))
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
		if err != nil {
			return nil, err
		}
		client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
			if cfg.Endpoint != "" {
				options.BaseEndpoint = aws.String(cfg.Endpoint)
			}
			options.UsePathStyle = cfg.ForcePathStyle
		})
		return NewS3(client, cfg.Bucket, policy), nil
	case "oss":
		if cfg.Bucket == "" || cfg.Region == "" {
			return nil, errors.New("OSS storage requires bucket and region")
		}
		provider := osscredentials.CredentialsProvider(osscredentials.NewEnvironmentVariableCredentialsProvider())
		if cfg.AccessKeyID != "" {
			provider = osscredentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret)
		}
		ossCfg := oss.LoadDefaultConfig().WithRegion(cfg.Region).WithCredentialsProvider(provider)
		if cfg.Endpoint != "" {
			ossCfg = ossCfg.WithEndpoint(cfg.Endpoint)
		}
		return NewOSS(ossCfg, cfg.Bucket, policy), nil
	default:
		return nil, errors.New("unsupported storage driver")
	}
}
