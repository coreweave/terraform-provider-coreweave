package coreweave

import (
	"context"
	"net/http"
	"sync"
	"time"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/go-cleanhttp"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	// We use global variables here because the ConfigureProvider RPC is called on every Create/Update/Delete
	// operation within a plan/apply which leads to the provider rebuilding a fresh client on each Terraform operation.
	// Using global variables allows the s3 client to be persisted across Terraform operations
	// and prevents us from generating excess access keys
	s3Mu              sync.Mutex
	singletonS3Client *s3.Client
	s3AccessKeyInfo   *cwobjectv1.CreateAccessKeyFromJWTResponse
)

func (c *Client) s3HttpClient() *http.Client {
	rc := retryablehttp.NewClient()
	rc.HTTPClient.Timeout = 30 * time.Second
	// cleanhttp.DefaultTransport disables keep-alives & idle connections
	// this helps us avoid S3 DNS caching, which can make creating/deleting buckets inconsistent
	rc.HTTPClient.Transport = cleanhttp.DefaultTransport()
	rc.RetryMax = 10
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 5 * time.Second
	// Jittered exponential back-off (min*2^n) with capping.
	rc.Backoff = retryablehttp.DefaultBackoff
	// Treat only idempotent verbs + 502/503/504 + transport errors as retryable.
	rc.CheckRetry = RetryPolicy

	return rc.StandardClient()
}

func (c *Client) createS3Client(ctx context.Context, zone string) (*s3.Client, *cwobjectv1.CreateAccessKeyFromJWTResponse, error) {
	resp, err := c.CreateAccessKeyFromJWT(ctx, connect.NewRequest(&cwobjectv1.CreateAccessKeyFromJWTRequest{
		DurationSeconds: wrapperspb.UInt32(60 * 15), // 15 minutes
	}))
	if err != nil {
		return nil, nil, err
	}

	httpClient := c.s3HttpClient()
	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithHTTPClient(httpClient),
		config.WithRegion(zone), // the zone specified here doesn't actually matter, as long as it's a valid DNS subdomain
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(resp.Msg.AccessKeyId, resp.Msg.SecretKey, "")),
	)
	if err != nil {
		return nil, nil, err
	}

	s3Client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = false
		o.BaseEndpoint = aws.String(c.s3Endpoint)
	})
	return s3Client, resp.Msg, nil
}

func (c *Client) S3Client(ctx context.Context, zone string) (*s3.Client, error) {
	s3Mu.Lock()
	defer s3Mu.Unlock()

	if s3AccessKeyInfo == nil || singletonS3Client == nil {
		tflog.Info(ctx, "creating new s3 client because one does not exist")
		client, keyInfo, err := c.createS3Client(ctx, zone)
		if err != nil {
			return nil, err
		}

		singletonS3Client = client
		s3AccessKeyInfo = keyInfo
		tflog.Info(ctx, "created new s3 client")
		return client, nil
	}

	// expiry is within 3 minutes from now (or already expired), refresh the client
	if time.Until(s3AccessKeyInfo.Expiry.AsTime()) <= 3*time.Minute {
		tflog.Info(ctx, "refreshing s3 client because keys expire within the next 3 minutes or have already expired")
		client, keyInfo, err := c.createS3Client(ctx, zone)
		if err != nil {
			return nil, err
		}

		singletonS3Client = client
		s3AccessKeyInfo = keyInfo
		tflog.Info(ctx, "refreshed s3 client")
		return client, nil
	}

	tflog.Info(ctx, "fetched cached s3 client")
	// otherwise use the already cached client
	return singletonS3Client, nil
}
