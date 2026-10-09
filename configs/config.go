package configs

import (
	"github.com/spf13/viper"
	"log"
	"os"
)

type Config struct {
	ServerPort     string
	PublicLogFile  string
	PrivateLogFile string
	ApiKey         string
	SecretKey      string
}

func GetConfig() Config {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatal("Error reading config file, %s", err)
	}

	viper.SetEnvPrefix("app")
	viper.AutomaticEnv()
	port := ""
	publicLogFile := ""
	privateLogFile := ""
	apiKey := ""
	secretKey := ""
	port = os.Getenv("SERVER_PORT")
	if port == "" {
		port = viper.GetString("server_port")
	}

	publicLogFile = os.Getenv("PUBLIC_LOG_FILE")
	if publicLogFile == "" {
		publicLogFile = viper.GetString("public_log_file")
	}

	privateLogFile = os.Getenv("PRIVATE_LOG_FILE")
	if privateLogFile == "" {
		privateLogFile = viper.GetString("private_log_file")
	}

	apiKey = os.Getenv("API_KEY")
	if apiKey == "" {
		apiKey = viper.GetString("api_key")
	}

	secretKey = os.Getenv("SECRET_KEY")
	if secretKey == "" {
		secretKey = viper.GetString("secret_key")
	}

	return Config{
		ServerPort:     port,
		PublicLogFile:  publicLogFile,
		PrivateLogFile: privateLogFile,
		ApiKey:         apiKey,
		SecretKey:      secretKey,
	}
}
