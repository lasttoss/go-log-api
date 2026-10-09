package services

import (
	"errors"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"log-api/configs"
	"log-api/internal/mappers"
	"time"
)

type LogService interface {
	PublicLog(req mappers.LogRequest)
	PrivateLog(req mappers.LogRequest, apiKey, secretKey string) error
}

type logService struct {
	config configs.Config
}

func (l logService) PublicLog(req mappers.LogRequest) {
	syncWriter := zapcore.AddSync(&lumberjack.Logger{
		Filename:   fmt.Sprintf(l.config.PublicLogFile, time.Now().Format("02-01-2006")),
		MaxSize:    10000,
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   true,
	})
	encoderConfig := zap.NewProductionEncoderConfig()
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	core := zapcore.NewCore(
		encoder,
		syncWriter,
		zap.InfoLevel, // Log level
	)
	logger := zap.New(core)
	logger.Info("",
		zap.String("user_id", req.UserId),
		zap.String("key", req.Key),
		zap.String("data", req.Data),
		zap.String("metadata", req.Metadata),
		zap.Int64("created_at", time.Now().UnixNano()))
}

func (l logService) PrivateLog(req mappers.LogRequest, apiKey, secretKey string) error {
	if apiKey != l.config.ApiKey {
		return errors.New("invalid api key")
	}

	if secretKey != l.config.SecretKey {
		return errors.New("invalid secret key")
	}

	syncWriter := zapcore.AddSync(&lumberjack.Logger{
		Filename:   fmt.Sprintf(l.config.PrivateLogFile, time.Now().Format("02-01-2006")),
		MaxSize:    10000,
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   true,
	})
	encoderConfig := zap.NewProductionEncoderConfig()
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	core := zapcore.NewCore(
		encoder,
		syncWriter,
		zap.InfoLevel, // Log level
	)
	logger := zap.New(core)
	logger.Info("",
		zap.String("user_id", req.UserId),
		zap.String("key", req.Key),
		zap.String("data", req.Data),
		zap.String("metadata", req.Metadata),
		zap.Int64("created_at", time.Now().UnixNano()))
	return nil
}

func NewLogService(config configs.Config) LogService {
	return &logService{config: config}
}
