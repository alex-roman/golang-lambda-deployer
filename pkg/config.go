package pkg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type DeployConfig struct {
	Env                string `json:"env"`
	AppName            string `json:"app_name"`
	LambdaName         string // Combined AppName and Env
	SourceCodeFilename string // LambdaName + commit + '.zip'; it appends -dirty if there are uncommitted changes
	BuildsBucket       string `json:"builds_bucket"`
	LogGroupName       string `json:"log_group_name"`
}

func LoadConfig() (DeployConfig, error) {
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

func LoadConfigOrDefaults() DeployConfig {
	config, _ := LoadConfig()

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

func getAppName() string {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Println("Error getting current directory:", err)
		os.Exit(1)
	}
	return filepath.Base(dir)
}
