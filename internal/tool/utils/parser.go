package utils

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func ParseRequiredString(raw map[string]json.RawMessage, name string) (string, error) {
	value, ok := raw[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}

	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	parsed = strings.TrimSpace(parsed)
	if parsed == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}

	return parsed, nil
}

func ParseOptionalPositiveInt(raw map[string]json.RawMessage, name string) (int, bool, error) {
	value, ok := raw[name]
	if !ok {
		return 0, false, nil
	}

	var parsed any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return 0, true, fmt.Errorf("%s must be a positive integer", name)
	}

	var text string
	switch typed := parsed.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = strings.TrimSpace(typed)
	default:
		return 0, true, fmt.Errorf("%s must be a positive integer", name)
	}

	integer, err := strconv.Atoi(text)
	if err != nil || integer < 1 {
		return 0, true, fmt.Errorf("%s must be a positive integer", name)
	}
	return integer, true, nil
}
