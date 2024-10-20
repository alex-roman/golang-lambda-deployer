package deployer

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

func (de *Deployer) Build() {
	currentGitCommit := getCurrentGitCommit()
	cmd := exec.Command("go", "build", "-ldflags", fmt.Sprintf("-s -w -X main.Commit=%s -X microservice.CommitHash=%s", currentGitCommit, currentGitCommit), "-o", "bootstrap", ".")
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
	de.Config.SourceCodeFilename = fmt.Sprintf("%s-%s.zip", de.Config.LambdaName, getCurrentGitCommit())
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

func getCurrentGitCommit() string {
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
