package logging

import (
	"context"
	"reflect"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Development       bool           `json:"development"`
	Level             string         `json:"level"`
	Encoding          string         `json:"encoding"`
	OutputPaths       []string       `json:"outputPaths"`
	ErrorOutputPaths  []string       `json:"errorOutputPaths"`
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
	zapConfig := zap.NewProductionConfig()
	if config.Development {
		zapConfig = zap.NewDevelopmentConfig()
	}

	if config.Level != "" {
		level, err := zapcore.ParseLevel(config.Level)
		if err != nil {
			return err
		}
		zapConfig.Level = zap.NewAtomicLevelAt(level)
	}

	if config.Encoding != "" {
		zapConfig.Encoding = config.Encoding
	}

	if len(config.OutputPaths) > 0 {
		zapConfig.OutputPaths = config.OutputPaths
	}

	if len(config.ErrorOutputPaths) > 0 {
		zapConfig.ErrorOutputPaths = config.ErrorOutputPaths
	}

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
