package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	specID      = regexp.MustCompile(`(?m)^\*\*ID:\*\* ((?:AC|FR)-\d+)\s*$`)
	acTag       = regexp.MustCompile(`^@AC-\d+$`)
	specTag     = regexp.MustCompile(`^@(?:AC|FR)-\d+$`)
	scenarioKey = regexp.MustCompile(`^\s*Scenario( Outline)?:`)
)

// checkAcceptanceTags сверяет теги .feature с openspec/specs (ADR 19): у
// сценария ровно один @AC-NNN, кроме @pending, и каждый @AC/@FR встречается в
// спецификациях ровно один раз.
func checkAcceptanceTags(root string) ([]string, error) {
	ids := map[string]int{}
	err := filepath.WalkDir(filepath.Join(root, "openspec/specs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "spec.md" {
			return err
		}
		spec, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range specID.FindAllSubmatch(spec, -1) {
			ids[string(m[1])]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var problems []string
	err = filepath.WalkDir(filepath.Join(root, "boundaries"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "target" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".feature" {
			return nil
		}
		feature, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		problems = append(problems, checkFeature(filepath.ToSlash(rel), string(feature), ids)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return problems, nil
}

func checkFeature(path, feature string, ids map[string]int) []string {
	var (
		problems []string
		tags     []string
	)
	for i, line := range strings.Split(feature, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "@"):
			tags = append(tags, strings.Fields(trimmed)...)
		case scenarioKey.MatchString(line):
			acs, pending := 0, false
			for _, tag := range tags {
				if acTag.MatchString(tag) {
					acs++
				}
				pending = pending || tag == "@pending"
				if specTag.MatchString(tag) && ids[tag[1:]] != 1 {
					problems = append(problems, fmt.Sprintf("%s:%d: %s встречается в openspec/specs %d раз", path, i+1, tag, ids[tag[1:]]))
				}
			}
			if acs != 1 && !pending {
				problems = append(problems, fmt.Sprintf("%s:%d: у сценария %d тегов @AC-NNN, нужен один", path, i+1, acs))
			}
			tags = nil
		case trimmed != "" && !strings.HasPrefix(trimmed, "#"):
			tags = nil
		}
	}
	return problems
}
