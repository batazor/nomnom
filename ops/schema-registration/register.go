package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
	"go.yaml.in/yaml/v3"
)

// compatibility is the global level of the Registry (FR-713): a new version
// must read and be read by every registered version.
const compatibility = "FULL_TRANSITIVE"

// eventFiles lists the Integration Event files and the buf modules that
// resolve their imports.
type eventFiles struct {
	Modules []string `yaml:"modules"`
	Files   []string `yaml:"files"`
}

func loadEventFiles(path string) (eventFiles, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return eventFiles{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg eventFiles
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return eventFiles{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if len(cfg.Modules) == 0 || len(cfg.Files) == 0 {
		return eventFiles{}, fmt.Errorf("%s must list modules and files", path)
	}
	return cfg, nil
}

// registrar registers the files of buf modules under root.
type registrar struct {
	root     string
	modules  []string
	registry registry
	// versions keeps the version of each file subject registered in this run.
	versions map[string]int
}

// run sets the compatibility level and registers every event file.
func (r *registrar) run(ctx context.Context, files []string) error {
	if err := r.registry.setCompatibility(ctx, compatibility); err != nil {
		return fmt.Errorf("set compatibility %s: %w", compatibility, err)
	}
	importPaths := make([]string, len(r.modules))
	for i, module := range r.modules {
		importPaths[i] = filepath.Join(r.root, module)
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: importPaths}),
	}
	compiled, err := compiler.Compile(ctx, files...)
	if err != nil {
		return fmt.Errorf("compile event files: %w", err)
	}
	for _, file := range compiled {
		if err := r.registerEvents(ctx, file); err != nil {
			return err
		}
	}
	return nil
}

// registerEvents registers the file under the subject of each top-level
// message: its full name is the event_type of the event (FR-717).
func (r *registrar) registerEvents(ctx context.Context, file protoreflect.FileDescriptor) error {
	s, err := r.schemaOf(ctx, file)
	if err != nil {
		return err
	}
	messages := file.Messages()
	if messages.Len() == 0 {
		return fmt.Errorf("event file %s declares no messages", file.Path())
	}
	for i := range messages.Len() {
		subject := string(messages.Get(i).FullName())
		if _, err := r.registry.register(ctx, subject, s); err != nil {
			return err
		}
		fmt.Printf("registered %s (%s)\n", subject, file.Path())
	}
	return nil
}

// schemaOf returns the source of the file with references to its imports;
// imported files of the modules are registered first under their path. The
// Registry knows the well-known types itself.
func (r *registrar) schemaOf(ctx context.Context, file protoreflect.FileDescriptor) (schema, error) {
	source, err := r.source(file.Path())
	if err != nil {
		return schema{}, err
	}
	s := schema{SchemaType: "PROTOBUF", Schema: source}
	imports := file.Imports()
	for i := range imports.Len() {
		imported := imports.Get(i).FileDescriptor
		if strings.HasPrefix(imported.Path(), "google/protobuf/") {
			continue
		}
		version, err := r.registerFile(ctx, imported)
		if err != nil {
			return schema{}, err
		}
		s.References = append(s.References, reference{Name: imported.Path(), Subject: imported.Path(), Version: version})
	}
	return s, nil
}

func (r *registrar) registerFile(ctx context.Context, file protoreflect.FileDescriptor) (int, error) {
	if version, ok := r.versions[file.Path()]; ok {
		return version, nil
	}
	s, err := r.schemaOf(ctx, file)
	if err != nil {
		return 0, err
	}
	version, err := r.registry.register(ctx, file.Path(), s)
	if err != nil {
		return 0, err
	}
	r.versions[file.Path()] = version
	fmt.Printf("registered %s\n", file.Path())
	return version, nil
}

// source reads the file from the module that contains it.
func (r *registrar) source(path string) (string, error) {
	for _, module := range r.modules {
		raw, err := os.ReadFile(filepath.Join(r.root, module, path))
		if err == nil {
			return string(raw), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
	}
	return "", fmt.Errorf("%s is in none of the modules", path)
}
