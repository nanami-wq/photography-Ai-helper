package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	App   AppConfig   `mapstructure:"app"`
	JWT   JWTConfig   `mapstructure:"jwt"`
	MySQL MySQLConfig `mapstructure:"mysql"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Port string `mapstructure:"port"`
	URL  string `mapstructure:"url"`
}

type JWTConfig struct {
	Secret      string `mapstructure:"secret"`
	AccessHours int    `mapstructure:"access_hours"`
}

type MySQLConfig struct {
	Host     string `mapstructure:"host"`
	Port     string `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Database string `mapstructure:"database"`
}

var current *Config

func Load() (*Config, error) {
	viper.SetConfigName("app")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Println("Error reading config file:", err)
		if _, statErr := os.Stat("./config/app.yaml"); os.IsNotExist(statErr) {
			return nil, fmt.Errorf("./config/app.yaml not found: %w", err)
		}
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}

	current = &cfg
	return current, nil
}

func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

func GetConfig() *Config {
	if current == nil {
		return MustLoad()
	}
	return current
}
