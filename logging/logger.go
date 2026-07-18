package logging

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Output string

const (
	OutputStdout Output = "stdout"
	OutputStderr Output = "stderr"
	OutputFile   Output = "file"
)

type Config struct {
	Development       bool           `json:"development"`
	Level             string         `json:"level"`
	Encoding          string         `json:"encoding"`
	Output            Output         `json:"output"`
	FilePath          string         `json:"filePath"`
	InitialFields     map[string]any `json:"initialFields"`
	DisableCaller     bool           `json:"disableCaller"`
	DisableStacktrace bool           `json:"disableStacktrace"`
}

type contextFieldsKey struct{}

type registeredContextField struct {
	contextKey any
	fieldName  string
}

var (
	mu                      sync.RWMutex
	logger                  = zap.Must(zap.NewProduction())
	registeredContextFields []registeredContextField
)

func Configure(config Config) error {
	outputPath, err := config.outputPath()
	if err != nil {
		return err
	}

	zapConfig := zap.NewProductionConfig()
	if config.Development {
		zapConfig = zap.NewDevelopmentConfig()
	}

	if config.Level != "" {
		level, err := zapcore.ParseLevel(config.Level)
		if err != nil {
			return fmt.Errorf("parse log level %q: %w", config.Level, err)
		}
		zapConfig.Level = zap.NewAtomicLevelAt(level)
	}

	if config.Encoding != "" {
		zapConfig.Encoding = config.Encoding
	}

	zapConfig.OutputPaths = []string{outputPath}
	zapConfig.ErrorOutputPaths = []string{string(OutputStderr)}

	zapConfig.InitialFields = config.InitialFields
	zapConfig.DisableCaller = config.DisableCaller
	zapConfig.DisableStacktrace = config.DisableStacktrace

	configuredLogger, err := zapConfig.Build()
	if err != nil {
		return err
	}

	SetLogger(configuredLogger)
	return nil
}

func (config Config) outputPath() (string, error) {
	output := Output(strings.ToLower(strings.TrimSpace(string(config.Output))))
	if output == "" {
		output = OutputStdout
	}

	switch output {
	case OutputStdout, OutputStderr:
		if strings.TrimSpace(config.FilePath) != "" {
			return "", fmt.Errorf("logging: filePath is only valid when output is %q", OutputFile)
		}
		return string(output), nil
	case OutputFile:
		filePath := strings.TrimSpace(config.FilePath)
		if filePath == "" {
			return "", fmt.Errorf("logging: filePath is required when output is %q", OutputFile)
		}
		return filePath, nil
	default:
		return "", fmt.Errorf("logging: unsupported output %q, expected %q, %q, or %q", config.Output, OutputStdout, OutputStderr, OutputFile)
	}
}

func SetLogger(configuredLogger *zap.Logger) {
	if configuredLogger == nil {
		configuredLogger = zap.NewNop()
	}

	mu.Lock()
	defer mu.Unlock()
	logger = configuredLogger
}

func Log(ctx context.Context) *zap.Logger {
	mu.RLock()
	configuredLogger := logger
	registeredFields := append([]registeredContextField(nil), registeredContextFields...)
	mu.RUnlock()

	fields := fieldsFromContext(ctx, registeredFields)
	if len(fields) == 0 {
		return configuredLogger
	}

	return configuredLogger.With(fields...)
}

func With(ctx context.Context, key string, value any) context.Context {
	return WithFields(ctx, zap.Any(key, value))
}

func WithFields(ctx context.Context, fields ...zap.Field) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	existingFields, _ := ctx.Value(contextFieldsKey{}).([]zap.Field)
	contextFields := make([]zap.Field, 0, len(existingFields)+len(fields))
	contextFields = append(contextFields, existingFields...)
	contextFields = append(contextFields, fields...)

	return context.WithValue(ctx, contextFieldsKey{}, contextFields)
}

func RegisterContextField(contextKey any, fieldName string) {
	validateContextKey(contextKey)
	if fieldName == "" {
		panic("logging: fieldName must not be empty")
	}

	mu.Lock()
	defer mu.Unlock()

	for i := range registeredContextFields {
		if registeredContextFields[i].contextKey == contextKey {
			registeredContextFields[i].fieldName = fieldName
			return
		}
	}

	registeredContextFields = append(registeredContextFields, registeredContextField{
		contextKey: contextKey,
		fieldName:  fieldName,
	})
}

func Sync() error {
	mu.RLock()
	configuredLogger := logger
	mu.RUnlock()

	return configuredLogger.Sync()
}

func fieldsFromContext(ctx context.Context, registeredFields []registeredContextField) []zap.Field {
	if ctx == nil {
		return nil
	}

	fields, _ := ctx.Value(contextFieldsKey{}).([]zap.Field)
	if len(registeredFields) == 0 {
		return fields
	}

	contextFields := make([]zap.Field, 0, len(fields)+len(registeredFields))
	contextFields = append(contextFields, fields...)

	for _, registeredField := range registeredFields {
		value := ctx.Value(registeredField.contextKey)
		if value != nil {
			contextFields = append(contextFields, zap.Any(registeredField.fieldName, value))
		}
	}

	return contextFields
}

func validateContextKey(contextKey any) {
	contextKeyType := reflect.TypeOf(contextKey)
	if contextKeyType == nil {
		panic("logging: contextKey must not be nil")
	}

	if !contextKeyType.Comparable() {
		panic("logging: contextKey must be comparable")
	}
}
