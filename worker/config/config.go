package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

type YtdlServiceConfig struct{}

type Config struct {
	Downly struct {

		// namespace
		Namespace string `yaml:"namespace" env:"NAMESPACE"`

		// rabbitmq config
		RabbitMQ struct {
			Host              string `yaml:"host" env:"HOST"`
			Port              string `yaml:"port" env:"PORT"`
			Username          string `yaml:"username" env:"USERNAME"`
			Password          string `yaml:"password" env:"PASSWORD"`
			HeartBeatInterval int    `yaml:"heartbeat_interval" env:"HEART_BEAT_INTERVAL"`
		} `yaml:"rabbitmq" envPrefix:"RABBITMQ__"`

		// worker config
		Worker struct {
			NumberOfWorkers int `yaml:"number_of_workers" env:"NUMBER_OF_WORKERS"`
		} `yaml:"worker" envPrefix:"WORKER__"`

		// services
		Services struct {
			Cobalt struct {
				Enable bool   `yaml:"enable" env:"ENABLE"`
				ApiUrl string `yaml:"api_url" env:"API_URL"`
			} `yaml:"cobalt" envPrefix:"COBALT__"`
			Ytdl struct {
				Enable bool   `yaml:"enable" env:"ENABLE"`
				Bin    string `yaml:"bin" env:"BIN"`
			} `yaml:"ytdl" envPrefix:"YTDL__"`
		} `yaml:"services" envPrefix:"SERVICES__"`
	} `yaml:"downly" envPrefix:"DOWNLY__"`
}

// ConfigS singleton global config var
var ConfigS *Config

// LoadConfig loading config from the yaml file
// and store in singleton var

func LoadConfig(configPath string) (*Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	config := &Config{}
	fullPath := filepath.Join(cwd, configPath)
	fileByte, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(fileByte, config)
	if err != nil {
		return nil, err
	}
	ConfigS = config
	return config, nil
}
