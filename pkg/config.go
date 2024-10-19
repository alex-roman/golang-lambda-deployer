package pkg

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-envparse"
	"github.com/mitchellh/mapstructure"
)

type DeployConfig struct {
	Env                string `json:"env" env:"ENV"  mapstructure:"ENV"`
	AppName            string `json:"app_name" env:"APP_NAME"  mapstructure:"APP_NAME"`
	LambdaName         string // Combined AppName and Env
	SourceCodeFilename string // LambdaName + commit + '.zip'; it appends -dirty if there are uncommitted changes
	BuildsBucket       string `json:"builds_bucket" env:"BUILDS_BUCKET"  mapstructure:"BUILDS_BUCKET"`
	LogGroupName       string `json:"log_group_name" env:"LOG_GROUP_NAME"  mapstructure:"LOG_GROUP_NAME"`
}

func LoadConfig() (DeployConfig, error) {
	var config DeployConfig

	configFile := "deploy.conf"
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return DeployConfig{}, fmt.Errorf("config file %s does not exist", configFile)
	}

	fileReader, err := openConfigFile(configFile)
	if err != nil {
		return DeployConfig{}, fmt.Errorf("error opening config file %s: %w", configFile, err)
	}

	configHashmap, err := envparse.Parse(fileReader)
	if err != nil {
		return DeployConfig{}, fmt.Errorf("error parsing config file %s: %w", configFile, err)
	}

	err = mapstructure.Decode(configHashmap, &config)
	if err != nil {
		return DeployConfig{}, fmt.Errorf("error decoding config hashmap: %w", err)
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

func openConfigFile(configFile string) (io.Reader, error) {
	f, err := os.Open(configFile)
	if err != nil {
		return nil, fmt.Errorf("error opening config file %s: %w", configFile, err)
	}
	return f, nil
}
