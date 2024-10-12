package deployer

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func (de *Deployer) TailLogs() {
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
			fmt.Println("Logs streaming session started")
			continue // ignore session start
		case *cwtypes.StartLiveTailResponseStreamMemberSessionUpdate:
			for _, logEvent := range e.Value.SessionResults {
				log.Println(*logEvent.Message)
			}
		default:
			// Handle on-stream exceptions
			if err := stream.Err(); err != nil {
				log.Fatalf("Error occured during streaming: %v", err)
			} else if event == nil {
				return
			} else {
				log.Fatalf("Unknown event type: %T", e)
			}
		}
	}
}
