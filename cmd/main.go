package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/spf13/cobra"
)

var env string

type DeployConfig struct {
	Env                string `json:"env"`
	AppName            string `json:"app_name"`
	LambdaName         string // Combined AppName and Env
	SourceCodeFilename string // LambdaName + commit + '.zip'; it appends -dirty if there are uncommitted changes
	BuildsBucket       string `json:"builds_bucket"`
	LogGroupName       string `json:"log_group_name"`
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
	// rootCmd.Flags().BoolVar(&tail, "tail", false, "Tail logs after deployment")

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
		fmt.Printf("Lambda function '%s' does not exist\n", de.Config.LambdaName)
		fmt.Printf("Available functions are: %s\n", strings.Join(availableFunctions, ", "))
		os.Exit(1)
	}

	availableBuckets := de.getAvailableBuckets()
	if !contains(availableBuckets, de.Config.BuildsBucket) {
		fmt.Printf("S3 bucket %s does not exist\n", de.Config.BuildsBucket)
		fmt.Printf("Available buckets are: %s\n", strings.Join(availableBuckets, ", "))
		os.Exit(1)
	}

	// Build and deploy the function
	de.buildAndDeploy()
	de.tailLogs()
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

	fmt.Println("Architecture: ", output.Architectures)
	if output.Architectures[0] == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func (de *Deployer) buildAndDeploy() {
	fmt.Println("Building and deploying...")
	de.build()
	de.uploadToS3()
	de.deploy()
	de.tailLogs()
}

func (de *Deployer) build() {
	currentGitCommit := de.getCurrentGitCommit()
	cmd := exec.Command("go", "build", "-ldflags", fmt.Sprintf("-s -w -X main.commit=%s", currentGitCommit), "-o", "bootstrap", ".")
	cmd.Env = append(os.Environ(), fmt.Sprintf("GOARCH=%s", de.determineFunctionArch()), "CGO_ENABLED=0", "GOOS=linux")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Println("Error building the project:", err)
		os.Exit(1)
	}

	// Ensure the binary is executable
	if err := os.Chmod("bootstrap", 0755); err != nil {
		fmt.Println("Error setting executable permissions on binary:", err)
		os.Exit(1)
	}

	// Create the ZIP file
	de.Config.SourceCodeFilename = fmt.Sprintf("%s-%s.zip", de.Config.LambdaName, de.getCurrentGitCommit())
	zipFile, err := os.Create(de.Config.SourceCodeFilename)
	if err != nil {
		fmt.Println("Error creating zip file:", err)
		os.Exit(1)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)

	// Add the binary to the ZIP file
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

	// Ensure all data is written to the ZIP file
	if err := zipWriter.Close(); err != nil {
		fmt.Println("Error finalizing zip file:", err)
		os.Exit(1)
	}
}

func (de *Deployer) uploadToS3() {
	zipFile, err := os.Open(de.Config.SourceCodeFilename)
	if err != nil {
		fmt.Println("Error opening zip file:", err)
		os.Exit(1)
	}
	defer zipFile.Close()

	uploader := manager.NewUploader(de.S3Client)
	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket:               aws.String(de.Config.BuildsBucket),
		Key:                  aws.String(de.Config.SourceCodeFilename),
		Body:                 zipFile,
		ServerSideEncryption: types.ServerSideEncryptionAes256,
	})
	if err != nil {
		fmt.Println("Error uploading zip to S3:", err)
		os.Exit(1)
	}
	// if err := os.Remove(de.Config.SourceCodeFilename); err != nil {
	// 	fmt.Println("Error deleting zip file:", err)
	// }

	fmt.Printf("Released %s to %s\n", de.Config.SourceCodeFilename, de.Config.BuildsBucket)
}

func (de *Deployer) deploy() {
	input := &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(de.Config.LambdaName),
		S3Bucket:     aws.String(de.Config.BuildsBucket),
		S3Key:        aws.String(de.Config.SourceCodeFilename),
	}

	_, err := de.LambdaClient.UpdateFunctionCode(context.Background(), input)
	if err != nil {
		fmt.Println("Error updating Lambda function code:", err)
		os.Exit(1)
	}

	fmt.Println("Waiting for the function to be updated...")

	waiter := lambda.NewFunctionUpdatedV2Waiter(de.LambdaClient)
	err = waiter.Wait(context.Background(), &lambda.GetFunctionInput{
		FunctionName: aws.String(de.Config.LambdaName),
	}, 5*time.Minute)
	if err != nil {
		fmt.Println("Error waiting for function update:", err)
		os.Exit(1)
	}

	publishInput := &lambda.PublishVersionInput{
		FunctionName: aws.String(de.Config.LambdaName),
	}

	publishOutput, err := de.LambdaClient.PublishVersion(context.Background(), publishInput)
	if err != nil {
		fmt.Println("Error publishing new Lambda version:", err)
		os.Exit(1)
	}

	updateAliasInput := &lambda.UpdateAliasInput{
		FunctionName:    aws.String(de.Config.LambdaName),
		Name:            aws.String("canary"),
		FunctionVersion: publishOutput.Version,
	}

	_, err = de.LambdaClient.UpdateAlias(context.Background(), updateAliasInput)
	if err != nil {
		fmt.Println("Error updating Lambda alias 'canary':", err)
		os.Exit(1)
	}

	fmt.Printf("Published new version %s and updated alias 'canary' to point to it\n", *publishOutput.Version)
}

func (de *Deployer) getCurrentGitCommit() string {
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		fmt.Println("Error getting current Git commit:", err)
		os.Exit(1)
	}

	// Check for uncommitted changes
	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		fmt.Println("Error checking git status:", err)
		os.Exit(1)
	}

	commitStr := strings.TrimSpace(string(commit))[0:7]
	if len(status) > 0 {
		commitStr += "-dirty"
	}

	return commitStr
}

func (de *Deployer) tailLogs() {
	availableLogGroups := de.getAvailableLogGroups()
	if !contains(availableLogGroups, de.Config.LogGroupName) {
		fmt.Printf("Log group %s does not exist\n", de.Config.LogGroupName)
		fmt.Printf("Available log groups are: %s\n", strings.Join(availableLogGroups, ", "))
		os.Exit(1)
	}

	request := &cloudwatchlogs.StartLiveTailInput{
		LogGroupIdentifiers:   []string{de.getARNofLogGroup()},
		LogEventFilterPattern: aws.String(`-"START RequestId" -"REPORT RequestId" -"END RequestId" -"INIT_START Runtime" -"EXTENSION"`),
	}

	response, err := de.CloudwatchClient.StartLiveTail(context.Background(), request)
	if err != nil {
		log.Fatalf("Failed to start streaming: %v", err)
	}

	stream := response.GetStream()
	go handleEventStreamAsync(stream)

	// Close the stream (which ends the session) after a timeout
	time.Sleep(300 * time.Second)
	stream.Close()
	log.Println("Event stream closed")
}

func (de *Deployer) getAvailableLogGroups() []string {
	var logGroupNames []string
	var nextToken *string

	for {
		input := &cloudwatchlogs.DescribeLogGroupsInput{
			NextToken: nextToken,
		}
		output, err := de.CloudwatchClient.DescribeLogGroups(context.Background(), input)
		if err != nil {
			fmt.Println("Error describing log groups:", err)
			os.Exit(1)
		}

		for _, logGroup := range output.LogGroups {
			logGroupNames = append(logGroupNames, *logGroup.LogGroupName)
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
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
	config.LambdaName = fmt.Sprintf("%s-%s", config.AppName, config.Env)
	if config.LogGroupName == "" {
		config.LogGroupName = fmt.Sprintf("/aws/lambda/%s-%s", config.AppName, config.Env)
	}
	return config
}

func (de *Deployer) getARNofLogGroup() string {
	input := &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(de.Config.LogGroupName),
	}

	output, err := de.CloudwatchClient.DescribeLogGroups(context.Background(), input)
	if err != nil {
		fmt.Println("Error describing log groups:", err)
		os.Exit(1)
	}

	if len(output.LogGroups) == 0 {
		fmt.Printf("No log groups found for the given prefix %s\n", de.Config.LogGroupName)
		os.Exit(1)
	}

	arn := strings.TrimSuffix(*output.LogGroups[0].Arn, ":*")
	return arn
}

func handleEventStreamAsync(stream *cloudwatchlogs.StartLiveTailEventStream) {
	eventsChan := stream.Events()
	for {
		event := <-eventsChan
		switch e := event.(type) {
		case *cwtypes.StartLiveTailResponseStreamMemberSessionStart:
			log.Println("Received SessionStart event")
		case *cwtypes.StartLiveTailResponseStreamMemberSessionUpdate:
			for _, logEvent := range e.Value.SessionResults {
				log.Println(*logEvent.Message)
			}
		default:
			// Handle on-stream exceptions
			if err := stream.Err(); err != nil {
				log.Fatalf("Error occured during streaming: %v", err)
			} else if event == nil {
				log.Println("Stream is Closed")
				return
			} else {
				log.Fatalf("Unknown event type: %T", e)
			}
		}
	}
}
