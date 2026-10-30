// Command architecture генерирует каталог событий docs/events-catalog.md и
// сверяет модель LikeC4 с ops (ADR 44).
//
//	go run . -root ../..         записать каталог
//	go run . -root ../.. -check  каталог без диффа и модель совпадает с ops
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const catalogPath = "docs/events-catalog.md"

func main() {
	root := flag.String("root", "", "корень репозитория")
	check := flag.Bool("check", false, "только проверить, файлы не менять")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-root обязателен")
		os.Exit(2)
	}
	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	topics, err := readTopics(filepath.Join(root, "ops/kind/manifests/messaging/topics.yaml"))
	if err != nil {
		return err
	}
	events, err := readEvents(root)
	if err != nil {
		return err
	}
	model, err := os.ReadFile(filepath.Join(root, "docs/architecture/model.c4"))
	if err != nil {
		return err
	}
	consumers := parseConsumers(string(model))
	catalog := renderCatalog(topics, events, consumers)
	path := filepath.Join(root, catalogPath)
	if !check {
		if err := os.WriteFile(path, catalog, 0o644); err != nil {
			return err
		}
	}

	problems, err := checkModel(root, string(model), topics, consumers)
	if err != nil {
		return err
	}
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, catalog) {
			problems = append(problems, catalogPath+" устарел: запустите make arch-generate")
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		return fmt.Errorf("расхождений: %d", len(problems))
	}
	return nil
}
