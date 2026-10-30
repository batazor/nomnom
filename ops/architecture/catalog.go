package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// contexts — владельцы Integration Events: у каждого свой events.proto.
var contexts = []string{"orders", "restaurants", "delivery", "payments"}

const dlqSuffix = ".dlq"

// Topic — ресурс KafkaTopic. Служебные topics Kafka Connect и Schema Registry в
// каталог не входят: у них нет владельца среди контекстов.
type Topic struct {
	Name       string
	Partitions int
	Retention  string
}

func (t Topic) owner() string { return t.Name[:strings.Index(t.Name, ".")] }

// short — имя topic без префикса контекста и версии: order-events.
func short(topic string) string { return topic[strings.LastIndex(topic, ".")+1:] }

func readTopics(path string) ([]Topic, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var topics []Topic
	dec := yaml.NewDecoder(f)
	for {
		var doc struct {
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Partitions int               `yaml:"partitions"`
				Config     map[string]string `yaml:"config"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			return topics, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !strings.Contains(doc.Metadata.Name, ".v1.") {
			continue
		}
		t := Topic{Name: doc.Metadata.Name, Partitions: doc.Spec.Partitions}
		if ms := doc.Spec.Config["retention.ms"]; ms != "" {
			n, err := strconv.ParseInt(ms, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: retention.ms: %w", t.Name, err)
			}
			t.Retention = fmt.Sprintf("%d дней", n/86_400_000)
		}
		topics = append(topics, t)
	}
}

// Event — сообщение events.proto и его комментарий.
type Event struct {
	Name    string
	Comment string
}

var (
	streamInHeader  = regexp.MustCompile(`^// Integration Events стрима ([a-z]+\.v1\.[a-z-]+)`)
	streamInSection = regexp.MustCompile(`^// --- ([a-z]+\.v1\.[a-z-]+)`)
	message         = regexp.MustCompile(`^message (\w+) \{`)
)

func readEvents(root string) (map[string][]Event, error) {
	events := map[string][]Event{}
	for _, c := range contexts {
		src, err := os.ReadFile(filepath.Join(root, "boundaries", c, "infrastructure/grpc/nomnom", c, "v1/events.proto"))
		if err != nil {
			return nil, err
		}
		if err := parseEvents(string(src), events); err != nil {
			return nil, fmt.Errorf("%s: %w", c, err)
		}
	}
	return events, nil
}

// parseEvents раскладывает сообщения по topics: стрим задан заголовком файла
// или секцией `// --- <topic>`. Сообщение вне стрима — ошибка.
func parseEvents(src string, events map[string][]Event) error {
	var stream string
	var comment []string
	for _, line := range strings.Split(src, "\n") {
		if m := streamInHeader.FindStringSubmatch(line); m != nil {
			stream, comment = m[1], nil
			continue
		}
		if m := streamInSection.FindStringSubmatch(line); m != nil {
			stream, comment = m[1], nil
			continue
		}
		if text, ok := strings.CutPrefix(line, "// "); ok {
			comment = append(comment, text)
			continue
		}
		if m := message.FindStringSubmatch(line); m != nil {
			if stream == "" {
				return fmt.Errorf("сообщение %s вне стрима", m[1])
			}
			events[stream] = append(events[stream], Event{Name: m[1], Comment: strings.Join(comment, " ")})
		}
		comment = nil
	}
	return nil
}

var kafkaEdge = regexp.MustCompile(`(?m)^\s*nomnom\.cell1\.([\w.]+) -\[kafka\]-> nomnom\.cell1\.messaging\.\w+ '([^']+)'`)

// parseConsumers читает потребителей из связей -[kafka]-> модели: заголовок
// связи — список коротких имён topics. Ключ — короткое имя.
func parseConsumers(model string) map[string][]string {
	consumers := map[string][]string{}
	for _, m := range kafkaEdge.FindAllStringSubmatch(model, -1) {
		// Debezium публикует в topics, а не читает их.
		if strings.HasPrefix(m[1], "messaging.") {
			continue
		}
		for _, name := range strings.Split(m[2], ", ") {
			consumers[name] = append(consumers[name], processName(m[1]))
		}
	}
	for _, list := range consumers {
		slices.Sort(list)
	}
	return consumers
}

// processName переводит путь элемента модели в имя процесса.
func processName(path string) string {
	if path == "router" {
		return "Cosmo Router"
	}
	context, role, _ := strings.Cut(path, ".")
	if role == "api" {
		return context
	}
	return context + "-" + role
}

func renderCatalog(topics []Topic, events map[string][]Event, consumers map[string][]string) []byte {
	var b bytes.Buffer
	b.WriteString("# Каталог событий\n\n")
	b.WriteString("Сгенерирован `make arch-generate`, вручную не правится. Источники: [`topics.yaml`](../ops/kind/manifests/messaging/topics.yaml) — topics, `events.proto` контекстов — события, [`model.c4`](architecture/model.c4) — потребители. Правила — [Domain Events](domain-events.md), разбор DLQ — [DLQ](dlq.md).\n\n")
	b.WriteString("Все topics публикует Debezium из Outbox контекста-владельца ([ADR 37](adr/0037-outbox-delivery-via-debezium-cdc.md)).\n\n")

	names := map[string]bool{}
	for _, t := range topics {
		names[t.Name] = true
	}
	b.WriteString("| Topic | Владелец | Partitions | Потребители | DLQ |\n| --- | --- | --- | --- | --- |\n")
	for _, t := range topics {
		if strings.HasSuffix(t.Name, dlqSuffix) {
			continue
		}
		list := "нет"
		if c := consumers[short(t.Name)]; len(c) > 0 {
			list = "`" + strings.Join(c, "`, `") + "`"
		}
		dlq := "нет"
		if names[t.Name+dlqSuffix] {
			dlq = "есть"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %s | %s |\n", t.Name, t.owner(), t.Partitions, list, dlq)
	}
	b.WriteString("\nТопики `*-changes` — Change Notifications в JSON для Cosmo Router ([ADR 36](adr/0036-graphql-subscriptions-via-cosmo-streams.md)). `orders.v1.order-relationships` — внутренний стрим `orders` для синхронизации связей SpiceDB ([ADR 18](adr/0018-spicedb-for-authorization.md)).\n")

	for _, t := range topics {
		list := events[t.Name]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## `%s`\n\n| Событие | Описание |\n| --- | --- |\n", t.Name)
		for _, e := range list {
			fmt.Fprintf(&b, "| `%s` | %s |\n", e.Name, strings.ReplaceAll(e.Comment, "|", "\\|"))
		}
	}
	return b.Bytes()
}
