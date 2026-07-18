package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

//go:embed default.json
var defaultConfigJSON []byte

func Load() (Config, error) {
	defaultConfig, err := decode("embedded default", defaultConfigJSON, Config{})
	if err != nil {
		return Config{}, err
	}

	var config Config
	if configPath := os.Getenv(ConfigPathEnv); configPath != "" {
		config, err = loadFile(configPath, defaultConfig)
		if err != nil {
			return Config{}, err
		}

		return applyEnvOverrides(config)
	}

	userConfigPath, err := defaultUserConfigPath(defaultConfig)
	if err != nil {
		return Config{}, err
	}

	contents, expandedPath, err := readConfigFile(userConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return applyEnvOverrides(defaultConfig)
	}
	if err != nil {
		return Config{}, err
	}

	config, err = decode(expandedPath, contents, defaultConfig)
	if err != nil {
		return Config{}, err
	}

	return applyEnvOverrides(config)
}

func loadFile(configPath string, base Config) (Config, error) {
	contents, expandedPath, err := readConfigFile(configPath)
	if err != nil {
		return Config{}, err
	}

	return decode(expandedPath, contents, base)
}

func readConfigFile(configPath string) ([]byte, string, error) {
	expandedPath, err := expandPath(configPath)
	if err != nil {
		return nil, "", err
	}

	contents, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, expandedPath, fmt.Errorf("read config %q: %w", expandedPath, err)
	}

	return contents, expandedPath, nil
}

func decode(source string, contents []byte, base Config) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()

	config := base
	if err := decoder.Decode(&config); err != nil {
		return Config{}, formatDecodeError(source, err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("decode config %s: multiple JSON values", source)
	}

	if err := validate(config); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", source, err)
	}

	return config, nil
}

func validate(config Config) error {
	if config.UserConfigPath == "" {
		return errors.New("userConfigPath must not be empty")
	}

	return nil
}

func defaultUserConfigPath(config Config) (string, error) {
	envName, err := configValueEnvName("UserConfigPath")
	if err != nil {
		return "", err
	}

	if value, ok := os.LookupEnv(envName); ok {
		parsed, err := parseRequiredEnvString(envName, value)
		if err != nil {
			return "", err
		}

		return parsed, nil
	}

	return config.UserConfigPath, nil
}

func applyEnvOverrides(config Config) (Config, error) {
	if err := applyEnvOverridesValue(reflect.ValueOf(&config).Elem(), nil); err != nil {
		return Config{}, err
	}

	if err := validate(config); err != nil {
		return Config{}, fmt.Errorf("validate env overrides: %w", err)
	}

	return config, nil
}

func applyEnvOverridesValue(configValue reflect.Value, jsonPath []string) error {
	configType := configValue.Type()
	for i := 0; i < configValue.NumField(); i++ {
		structField := configType.Field(i)
		if !structField.IsExported() {
			continue
		}

		jsonName, ok := jsonFieldName(structField)
		if !ok {
			continue
		}

		fieldPath := appendJSONPath(jsonPath, jsonName)
		fieldValue := configValue.Field(i)
		if fieldValue.Kind() == reflect.Struct {
			if err := applyEnvOverridesValue(fieldValue, fieldPath); err != nil {
				return err
			}
			continue
		}

		envName := envNameForJSONPath(fieldPath)
		value, ok := os.LookupEnv(envName)
		if !ok {
			continue
		}

		if err := setFieldFromEnv(fieldValue, envName, value); err != nil {
			return err
		}
	}

	return nil
}

func setFieldFromEnv(fieldValue reflect.Value, envName string, value string) error {
	if !fieldValue.CanSet() {
		return fmt.Errorf("parse env %s: field cannot be set", envName)
	}

	switch fieldValue.Kind() {
	case reflect.Bool:
		parsed, err := parseEnvBool(envName, value)
		if err != nil {
			return err
		}
		fieldValue.SetBool(parsed)
	case reflect.String:
		fieldValue.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, fieldValue.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse env %s: invalid number %q", envName, value)
		}
		fieldValue.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(value, 10, fieldValue.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse env %s: invalid number %q", envName, value)
		}
		fieldValue.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(value, fieldValue.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse env %s: invalid number %q", envName, value)
		}
		fieldValue.SetFloat(parsed)
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Interface:
		return setFieldFromJSONEnv(fieldValue, envName, value)
	default:
		return fmt.Errorf("parse env %s: unsupported config field type %s", envName, fieldValue.Type())
	}

	return nil
}

func setFieldFromJSONEnv(fieldValue reflect.Value, envName string, value string) error {
	parsedValue := reflect.New(fieldValue.Type())
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(parsedValue.Interface()); err != nil {
		return formatEnvJSONError(envName, err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("parse env %s: multiple JSON values", envName)
	}

	parsed := parsedValue.Elem()
	if fieldValue.Kind() == reflect.Map && parsed.IsNil() {
		return fmt.Errorf("parse env %s: invalid type: got null, want object", envName)
	}

	fieldValue.Set(parsed)
	return nil
}

func parseRequiredEnvString(envName string, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("parse env %s: value must not be empty", envName)
	}

	return value, nil
}

func parseEnvBool(envName string, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse env %s: invalid boolean %q, want true or false", envName, value)
	}

	return parsed, nil
}

func configValueEnvName(goFieldPath ...string) (string, error) {
	configType := reflect.TypeOf(Config{})
	jsonPath := make([]string, 0, len(goFieldPath))

	for _, goFieldName := range goFieldPath {
		if configType.Kind() != reflect.Struct {
			return "", fmt.Errorf("config field %q is not nested under a struct", goFieldName)
		}

		structField, ok := configType.FieldByName(goFieldName)
		if !ok {
			return "", fmt.Errorf("config field %q does not exist", goFieldName)
		}

		jsonName, ok := jsonFieldName(structField)
		if !ok {
			return "", fmt.Errorf("config field %q has no JSON field name", goFieldName)
		}

		jsonPath = append(jsonPath, jsonName)
		configType = structField.Type
	}

	return envNameForJSONPath(jsonPath), nil
}

func configValueEnvNames() ([]string, error) {
	var envNames []string
	if err := collectConfigValueEnvNames(reflect.TypeOf(Config{}), nil, &envNames); err != nil {
		return nil, err
	}

	return envNames, nil
}

func collectConfigValueEnvNames(configType reflect.Type, jsonPath []string, envNames *[]string) error {
	if configType.Kind() != reflect.Struct {
		return fmt.Errorf("config type %s is not a struct", configType)
	}

	for i := 0; i < configType.NumField(); i++ {
		structField := configType.Field(i)
		if !structField.IsExported() {
			continue
		}

		jsonName, ok := jsonFieldName(structField)
		if !ok {
			continue
		}

		fieldPath := appendJSONPath(jsonPath, jsonName)
		if structField.Type.Kind() == reflect.Struct {
			if err := collectConfigValueEnvNames(structField.Type, fieldPath, envNames); err != nil {
				return err
			}
			continue
		}

		*envNames = append(*envNames, envNameForJSONPath(fieldPath))
	}

	return nil
}

func jsonFieldName(structField reflect.StructField) (string, bool) {
	tag := structField.Tag.Get("json")
	if tag == "-" {
		return "", false
	}

	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = structField.Name
	}

	return name, true
}

func appendJSONPath(jsonPath []string, name string) []string {
	fieldPath := make([]string, len(jsonPath)+1)
	copy(fieldPath, jsonPath)
	fieldPath[len(jsonPath)] = name
	return fieldPath
}

func envNameForJSONPath(jsonPath []string) string {
	envParts := make([]string, 0, len(jsonPath))
	for _, name := range jsonPath {
		envParts = append(envParts, jsonNameToEnvName(name))
	}

	return envPrefix + strings.Join(envParts, "_")
}

func jsonNameToEnvName(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	lastWasUnderscore := false

	for i, r := range runes {
		if r == '-' || r == '.' || r == '_' {
			if builder.Len() > 0 && !lastWasUnderscore {
				builder.WriteByte('_')
				lastWasUnderscore = true
			}
			continue
		}

		if unicode.IsUpper(r) && shouldInsertEnvSeparator(runes, i) {
			builder.WriteByte('_')
		}

		builder.WriteRune(unicode.ToUpper(r))
		lastWasUnderscore = false
	}

	return strings.Trim(builder.String(), "_")
}

func shouldInsertEnvSeparator(runes []rune, index int) bool {
	if index == 0 {
		return false
	}

	previous := runes[index-1]
	if previous == '-' || previous == '.' || previous == '_' {
		return false
	}
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}

	return unicode.IsUpper(previous) && index+1 < len(runes) && unicode.IsLower(runes[index+1])
}

func formatEnvJSONError(envName string, err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf(
			"parse env %s: invalid type: got %s, want %s",
			envName,
			typeErr.Value,
			expectedJSONType(typeErr.Type),
		)
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("parse env %s: invalid JSON at byte %d: %w", envName, syntaxErr.Offset, err)
	}

	if errors.Is(err, io.EOF) {
		return fmt.Errorf("parse env %s: empty JSON, want object", envName)
	}

	return fmt.Errorf("parse env %s: %w", envName, err)
}

func formatDecodeError(source string, err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Errorf(
			"decode config %s: field %q has invalid type: got %s, want %s",
			source,
			fieldName(typeErr),
			typeErr.Value,
			expectedJSONType(typeErr.Type),
		)
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("decode config %s: invalid JSON at byte %d: %w", source, syntaxErr.Offset, err)
	}

	if errors.Is(err, io.EOF) {
		return fmt.Errorf("decode config %s: empty JSON", source)
	}

	const unknownFieldPrefix = "json: unknown field "
	if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
		return fmt.Errorf("decode config %s: unknown field %s", source, strings.TrimPrefix(err.Error(), unknownFieldPrefix))
	}

	return fmt.Errorf("decode config %s: %w", source, err)
}

func fieldName(typeErr *json.UnmarshalTypeError) string {
	if typeErr.Field != "" {
		return typeErr.Field
	}

	return fmt.Sprintf("byte offset %d", typeErr.Offset)
}

func expectedJSONType(configType reflect.Type) string {
	if configType == nil {
		return "valid JSON"
	}

	switch configType.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Interface:
		return "any JSON value"
	default:
		return configType.String()
	}
}

func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}

		if path == "~" {
			return homeDir, nil
		}

		return filepath.Join(homeDir, strings.TrimPrefix(path, "~/")), nil
	}

	return path, nil
}
