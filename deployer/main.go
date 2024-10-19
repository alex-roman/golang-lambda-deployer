package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/alex-roman/golang-lambda-deployer/pkg"
	"github.com/alex-roman/golang-lambda-deployer/pkg/deployer"
	"github.com/spf13/cobra"
)

var env string
var tail bool

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
	de := deployer.Deployer{}
	de.Config = pkg.LoadConfigOrDefaults()
	de.InitAWSClient()

	availableFunctions := de.GetAvailableFunctions()
	if !contains(availableFunctions, de.Config.LambdaName) {
		fmt.Printf("Lambda function '%s' does not exist\n", de.Config.LambdaName)
		fmt.Printf("Available functions are: %s\n", strings.Join(availableFunctions, ", "))
		os.Exit(1)
	}

	availableBuckets := de.GetAvailableBuckets()
	if !contains(availableBuckets, de.Config.BuildsBucket) {
		fmt.Printf("S3 bucket %s does not exist\n", de.Config.BuildsBucket)
		fmt.Printf("Available buckets are: %s\n", strings.Join(availableBuckets, ", "))
		os.Exit(1)
	}

	// Build and deploy the function
	de.Build()
	de.Deploy()

	if tail {
		de.TailLogs()
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
