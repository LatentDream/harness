package logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	SessionIDPlaceholder = "{sessionId}"
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
	return ConfigureForSession(config, "")
}

func ConfigureForSession(config Config, sessionID string) error {
	outputPath, err := config.outputPath()
	if err != nil {
		return err
	}
	if strings.Contains(outputPath, SessionIDPlaceholder) {
		if sessionID == "" {
			return fmt.Errorf("logging: filePath contains %q but session ID is empty", SessionIDPlaceholder)
		}
		outputPath = strings.ReplaceAll(outputPath, SessionIDPlaceholder, sessionID)
	}
	outputPath, err = expandPath(outputPath)
	if err != nil {
		return err
	}
	if outputPath != string(OutputStdout) && outputPath != string(OutputStderr) {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return fmt.Errorf("create log directory: %w", err)
		}
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

func ConfigureOrExit(config Config) {
	if err := Configure(config); err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure logging: %v\n", err)
		os.Exit(1)
	}
}

func ConfigureOrExitForSession(config Config, sessionID string) {
	if err := ConfigureForSession(config, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure logging: %v\n", err)
		os.Exit(1)
	}
}

func (config Config) outputPath() (string, error) {
	output := Output(strings.ToLower(strings.TrimSpace(string(config.Output))))
	if output == "" {
		output = OutputStdout
	}

	switch output {
	case OutputStdout, OutputStderr:
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

func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
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
