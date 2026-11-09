package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"

	"nomnom/orders/pkg/config/database"
)

const (
	// EnvConfigFile — путь к файлу конфигурации процесса; обязателен.
	EnvConfigFile = "CONFIG_FILE"
	// APIVersion — версия формата файла. Несовместимое изменение spec
	// объявляется новой версией; процесс принимает только известные.
	APIVersion = "config.nomnom.dev/v1alpha1"
	// Kind — kind файла всех ролей orders.
	Kind = "Orders"
)

// file — файл конфигурации в форме ресурса Kubernetes; в Kubernetes API он не
// регистрируется.
type file struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   metadata `yaml:"metadata"`
	Spec       Config   `yaml:"spec"`
}

type metadata struct {
	Name string `yaml:"name"`
}

// read читает CONFIG_FILE: сверяет apiVersion и kind, передаёт metadata.name в
// checkName (nil — имя не сверяется), отклоняет неизвестные ключи на любом
// уровне, строго декодирует spec поверх значений по умолчанию и подставляет
// значения из env: секреты, POD_NAME и версию worker'а от Temporal Worker
// Controller.
func read(serviceName string, checkName func(name string) error) (Config, error) {
	path := os.Getenv(EnvConfigFile)
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("%s is required: the process reads its configuration only from the file", EnvConfigFile)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	var header struct {
		APIVersion string   `yaml:"apiVersion"`
		Kind       string   `yaml:"kind"`
		Metadata   metadata `yaml:"metadata"`
	}
	if err := document.Decode(&header); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if header.APIVersion != APIVersion || header.Kind != Kind {
		return Config{}, fmt.Errorf("config %s: apiVersion %q and kind %q expected, got %q and %q",
			path, APIVersion, Kind, header.APIVersion, header.Kind)
	}
	if checkName != nil {
		if err := checkName(header.Metadata.Name); err != nil {
			return Config{}, fmt.Errorf("config %s: %w", path, err)
		}
	}
	if err := errors.Join(unknownKeys(&document, reflect.TypeFor[file](), "")...); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}

	decoded := file{Spec: defaults(serviceName)}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&decoded); err != nil {
		return Config{}, fmt.Errorf("decode config %s: %w", path, err)
	}

	cfg := decoded.Spec
	cfg.Database.DSN = os.Getenv(database.EnvPostgresDSN)
	cfg.SpiceDB.PresharedKey = os.Getenv(EnvSpiceDBPresharedKey)
	cfg.Temporal.DeploymentName = os.Getenv(EnvTemporalDeploymentName)
	cfg.Temporal.BuildID = os.Getenv(EnvTemporalWorkerBuildID)
	cfg.Observability.Telemetry.InstanceID = os.Getenv(EnvPodName)
	return cfg, nil
}

// unknownKeys возвращает ошибку с путём ключа (spec.shutdown.drainTimout) для
// каждого ключа mapping, которому нет поля в типе t. Decoder с KnownFields
// тоже отклоняет такие ключи, но называет только строку и Go-тип.
func unknownKeys(node *yaml.Node, t reflect.Type, path string) []error {
	switch node.Kind {
	case yaml.DocumentNode:
		var errs []error
		for _, content := range node.Content {
			errs = append(errs, unknownKeys(content, t, path)...)
		}
		return errs
	case yaml.AliasNode:
		return unknownKeys(node.Alias, t, path)
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var errs []error
	switch {
	case t.Kind() == reflect.Struct && node.Kind == yaml.MappingNode:
		fields := yamlFields(t)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			fieldType, known := fields[key]
			if !known {
				errs = append(errs, fmt.Errorf("%s: unknown field", keyPath(path, key)))
				continue
			}
			errs = append(errs, unknownKeys(value, fieldType, keyPath(path, key))...)
		}
	case t.Kind() == reflect.Map && node.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			errs = append(errs, unknownKeys(node.Content[i+1], t.Elem(), keyPath(path, node.Content[i].Value))...)
		}
	case (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && node.Kind == yaml.SequenceNode:
		for i, item := range node.Content {
			errs = append(errs, unknownKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return errs
}

// yamlFields — ключи YAML полей структуры по правилам yaml.v3: имя из тега,
// иначе имя поля в нижнем регистре; "-" пропускается, inline раскрывается.
func yamlFields(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if strings.Contains(options, "inline") {
			maps.Copy(fields, yamlFields(field.Type))
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		fields[name] = field.Type
	}
	return fields
}

func keyPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
