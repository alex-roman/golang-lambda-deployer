package deployer

import (
	"context"
	"fmt"
	"os"

	"github.com/alex-roman/golang-lambda-deployer/pkg"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Deployer struct {
	Config           pkg.DeployConfig
	LambdaClient     *lambda.Client
	CloudwatchClient *cloudwatchlogs.Client
	S3Client         *s3.Client
}

func (de *Deployer) InitAWSClient() {
	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion("us-east-1"))
	if err != nil {
		fmt.Println("Error loading AWS configuration:", err)
		os.Exit(1)
	}

	de.CloudwatchClient = cloudwatchlogs.NewFromConfig(cfg)
	de.LambdaClient = lambda.NewFromConfig(cfg)
	de.S3Client = s3.NewFromConfig(cfg)
}

func (de *Deployer) GetAvailableFunctions() []string {
	var functionNames []string
	var nextMarker *string

	for {
		input := &lambda.ListFunctionsInput{
			Marker: nextMarker,
		}
		output, err := de.LambdaClient.ListFunctions(context.Background(), input)
		if err != nil {
			fmt.Println("Error listing Lambda functions:", err)
			os.Exit(1)
		}

		for _, function := range output.Functions {
			functionNames = append(functionNames, *function.FunctionName)
		}

		if output.NextMarker == nil {
			break
		}
		nextMarker = output.NextMarker
	}

	return functionNames
}

func (de *Deployer) GetAvailableBuckets() []string {
	input := &s3.ListBucketsInput{}
	output, err := de.S3Client.ListBuckets(context.Background(), input)
	if err != nil {
		fmt.Println("Error listing S3 buckets:", err)
		os.Exit(1)
	}

	var bucketNames []string
	for _, bucket := range output.Buckets {
		bucketNames = append(bucketNames, *bucket.Name)
	}
	return bucketNames
}
