package deployer

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func (de *Deployer) Deploy() {
	de.uploadToS3()

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
