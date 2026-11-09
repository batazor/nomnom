package config

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

var (
	documentType = reflect.TypeFor[document]()
	durationType = reflect.TypeFor[time.Duration]()
)

// checkKnownFields сверяет ключи файла со структурой document и возвращает ошибку
// с путём ключа (spec.shutdown.drainTimout: unknown field). KnownFields декодера
// называет только поле и тип, а оператору нужен путь в файле. Длительности —
// только строки Go-формата: целое число декодер принял бы за наносекунды.
func checkKnownFields(node *yaml.Node, t reflect.Type, path string) error {
	switch {
	case t == durationType:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return fmt.Errorf("%s: duration must be a string such as 500ms, 5s or 2m", path)
		}
	case t.Kind() == reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: must be a mapping", rootPath(path))
		}
		fields := yamlFields(t)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			field, ok := fields[key]
			if !ok {
				return fmt.Errorf("%s: unknown field", join(path, key))
			}
			if err := checkKnownFields(value, field, join(path, key)); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Map:
		if node.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: must be a mapping", path)
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := checkKnownFields(node.Content[i+1], t.Elem(), join(path, node.Content[i].Value)); err != nil {
				return err
			}
		}
	}
	return nil
}

// yamlFields — ключи структуры по тегам yaml; поля с тегом "-" в файле не задаются.
func yamlFields(t reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "-" || name == "" {
			continue
		}
		fields[name] = field.Type
	}
	return fields
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func rootPath(path string) string {
	if path == "" {
		return "document"
	}
	return path
}
