package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/spf13/cobra"
)

var env string
var tail bool

type DeployConfig struct {
	Env          string `json:"env"`
	AppName      string `json:"app_name"`
	LambdaName   string // Combined AppName and Env
	BuildsBucket string `json:"builds_bucket"`
	LogGroupName string `json:"log_group_name"`
}

type Deployer struct {
	Config           DeployConfig
	LambdaClient     *lambda.Client
	CloudwatchClient *cloudwatchlogs.Client
	S3Client         *s3.Client
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a Lambda function",
		Run:   runDeploy,
	}

	rootCmd.Flags().StringVarP(&env, "env", "e", "", "Environment name postfix (prod-use1|stag)")
	rootCmd.Flags().BoolVar(&tail, "tail", false, "Tail logs after deployment")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runDeploy(cmd *cobra.Command, args []string) {
	de := Deployer{}
	de.Config = loadConfigOrDefaults()
	de.initAWSClient()

	availableFunctions := de.getAvailableFunctions()
	if !contains(availableFunctions, de.Config.LambdaName) {
		fmt.Printf("Lambda function %s does not exist\n", de.Config.LambdaName)
		fmt.Printf("Available functions are: %s\n", strings.Join(availableFunctions, ", "))
		os.Exit(1)
	}

	availableBuckets := de.getAvailableBuckets()
	if !contains(availableBuckets, de.Config.BuildsBucket) {
		fmt.Printf("S3 bucket %s does not exist\n", de.Config.BuildsBucket)
		fmt.Printf("Available buckets are: %s\n", strings.Join(availableBuckets, ", "))
		os.Exit(1)
	}

	goarch := de.determineFunctionArch()
	fmt.Printf("Selected: GOARCH=%s\n", goarch)

	// Build and deploy the function
	de.buildAndDeploy(goarch)

	if tail {
		de.tailLogs(de.Config)
	}
}

func getAppName() string {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Println("Error getting current directory:", err)
		os.Exit(1)
	}
	return filepath.Base(dir)
}

func (de *Deployer) initAWSClient() {
	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion("us-east-1"))
	if err != nil {
		fmt.Println("Error loading AWS configuration:", err)
		os.Exit(1)
	}

	de.CloudwatchClient = cloudwatchlogs.NewFromConfig(cfg)
	de.LambdaClient = lambda.NewFromConfig(cfg)
	de.S3Client = s3.NewFromConfig(cfg)
}

func (de *Deployer) getAvailableFunctions() []string {
	input := &lambda.ListFunctionsInput{}
	output, err := de.LambdaClient.ListFunctions(context.Background(), input)
	if err != nil {
		fmt.Println("Error listing Lambda functions:", err)
		os.Exit(1)
	}

	var functionNames []string
	for _, function := range output.Functions {
		functionNames = append(functionNames, *function.FunctionName)
	}
	return functionNames
}

func (de *Deployer) getAvailableBuckets() []string {
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

func (de *Deployer) determineFunctionArch() string {
	input := &lambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(de.Config.LambdaName),
	}

	output, err := de.LambdaClient.GetFunctionConfiguration(context.Background(), input)
	if err != nil {
		fmt.Println("Error getting function configuration:", err)
		os.Exit(1)
	}

	return string(output.Architectures[0])
}

func (de *Deployer) buildAndDeploy(goarch string) {
	fmt.Println("Building and deploying...")
	de.build(goarch)
	de.deploy()
}

func (de *Deployer) build(goarch string) {
	currentGitCommit := de.getCurrentGitCommit()
	cmd := exec.Command("go", "build", "-ldflags", fmt.Sprintf("-s -w -X main.commit=%s", currentGitCommit), "-o", "bootstrap", ".")
	cmd.Env = append(os.Environ(), fmt.Sprintf("GOARCH=%s", goarch))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Println("Error building the project:", err)
		os.Exit(1)
	}

	zipFile, err := os.Create("bootstrap.zip")
	if err != nil {
		fmt.Println("Error creating zip file:", err)
		os.Exit(1)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	binaryFile, err := os.Open("bootstrap")
	if err != nil {
		fmt.Println("Error opening binary file:", err)
		os.Exit(1)
	}
	defer binaryFile.Close()

	w, err := zipWriter.Create("bootstrap")
	if err != nil {
		fmt.Println("Error creating zip entry:", err)
		os.Exit(1)
	}

	if _, err := io.Copy(w, binaryFile); err != nil {
		fmt.Println("Error writing binary to zip:", err)
		os.Exit(1)
	}

	zipFile.Seek(0, 0)

	uploader := manager.NewUploader(de.S3Client)
	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(de.Config.BuildsBucket),
		Key:    aws.String("bootstrap.zip"),
		Body:   zipFile,
	})
	if err != nil {
		fmt.Println("Error uploading zip to S3:", err)
		os.Exit(1)
	}
}

func (de *Deployer) getCurrentGitCommit() string {
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		fmt.Println("Error getting current Git commit:", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(commit))
}

func (de *Deployer) tailLogs(deployConfig DeployConfig) {
	availableLogGroups := de.getAvailableLogGroups()
	if !contains(availableLogGroups, deployConfig.LogGroupName) {
		fmt.Printf("Log group %s does not exist\n", deployConfig.LogGroupName)
		fmt.Printf("Available log groups are: %s\n", strings.Join(availableLogGroups, ", "))
		os.Exit(1)
	}

	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: aws.String(deployConfig.LogGroupName),
	}

	stream, err := de.CloudwatchClient.FilterLogEvents(context.Background(), input)
	if err != nil {
		fmt.Println("Error filtering log events:", err)
		os.Exit(1)
	}

	for _, event := range stream.Events {
		fmt.Printf("[%s] %s\n", *event.Timestamp, *event.Message)
	}
}

func (de *Deployer) getAvailableLogGroups() []string {
	input := &cloudwatchlogs.DescribeLogGroupsInput{}
	output, err := de.CloudwatchClient.DescribeLogGroups(context.Background(), input)
	if err != nil {
		fmt.Println("Error describing log groups:", err)
		os.Exit(1)
	}

	var logGroupNames []string
	for _, logGroup := range output.LogGroups {
		logGroupNames = append(logGroupNames, *logGroup.LogGroupName)
	}
	return logGroupNames
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func loadConfig() (DeployConfig, error) {
	configFile := "deploy.conf"
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return DeployConfig{}, fmt.Errorf("config file %s does not exist", configFile)
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		return DeployConfig{}, fmt.Errorf("error reading config file %s: %w", configFile, err)
	}

	var config DeployConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return DeployConfig{}, fmt.Errorf("error unmarshalling config file %s: %w", configFile, err)
	}

	return config, nil
}

func loadConfigOrDefaults() DeployConfig {
	config, _ := loadConfig()

	if config.Env == "" {
		config.Env = "stag"
	}
	if config.AppName == "" {
		config.AppName = getAppName()
	}
	if config.BuildsBucket == "" {
		config.BuildsBucket = "e4f-builds"
	}
	if config.LogGroupName == "" {
		config.LogGroupName = fmt.Sprintf("/aws/lambda/%s-%s", config.AppName, config.Env)
	}
	return config
}
