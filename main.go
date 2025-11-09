package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: test <path_to_yaml>")
		os.Exit(1)
	}

	filePath := os.Args[1]
	content, err := os.ReadFile(filePath)
	if err != nil {
		printErr(filePath, 1, fmt.Sprintf("cannot read file: %v", err))
		os.Exit(1)
	}

	// --- Исправление кодировки ---
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		content = content[3:] // Удаляем BOM
	}
	if !isUTF8(content) {
		decoded := make([]rune, len(content))
		for i, b := range content {
			decoded[i] = rune(b) // грубое преобразование CP1251 → UTF-8
		}
		content = []byte(string(decoded))
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		printErr(filePath, 1, fmt.Sprintf("cannot unmarshal file: %v", err))
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		printErr(filePath, 1, "empty YAML")
		os.Exit(1)
	}
	doc := root.Content[0]

	validatePod(filePath, doc)
	os.Exit(0)
}

// === Основная валидация ===

func validatePod(file string, doc *yaml.Node) {
	apiVersion := findFieldValue(doc, "apiVersion")
	if apiVersion == nil {
		printErr(file, 1, "apiVersion is required")
		os.Exit(1)
	}
	if apiVersion.Value != "v1" {
		printErr(file, apiVersion.Line, fmt.Sprintf("apiVersion has unsupported value '%s'", apiVersion.Value))
		os.Exit(1)
	}

	kind := findFieldValue(doc, "kind")
	if kind == nil {
		printErr(file, 1, "kind is required")
		os.Exit(1)
	}
	if kind.Value != "Pod" {
		printErr(file, kind.Line, fmt.Sprintf("kind has unsupported value '%s'", kind.Value))
		os.Exit(1)
	}

	meta := findFieldValue(doc, "metadata")
	if meta == nil {
		printErr(file, 1, "metadata is required")
		os.Exit(1)
	}
	name := findFieldValue(meta, "name")
	if name == nil {
		printErr(file, 1, "metadata.name is required")
		os.Exit(1)
	}

	spec := findFieldValue(doc, "spec")
	if spec == nil {
		printErr(file, 1, "spec is required")
		os.Exit(1)
	}

	osNode := findFieldValue(spec, "os")
	if osNode != nil && osNode.Value != "linux" && osNode.Value != "windows" {
		printErr(file, osNode.Line, fmt.Sprintf("os has unsupported value '%s'", osNode.Value))
		os.Exit(1)
	}

	containers := findFieldValue(spec, "containers")
	if containers == nil || len(containers.Content) == 0 {
		printErr(file, spec.Line, "containers is required")
		os.Exit(1)
	}

	for _, container := range containers.Content {
		validateContainer(file, container)
	}
}

func validateContainer(file string, node *yaml.Node) {
	name := findFieldValue(node, "name")
	if name == nil {
		printErr(file, node.Line, "container.name is required")
		os.Exit(1)
	}
	if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(name.Value) {
		printErr(file, name.Line, fmt.Sprintf("container.name has invalid format '%s'", name.Value))
		os.Exit(1)
	}

	image := findFieldValue(node, "image")
	if image == nil {
		printErr(file, node.Line, "image is required")
		os.Exit(1)
	}
	if !strings.HasPrefix(image.Value, "registry.bigbrother.io/") || !strings.Contains(image.Value, ":") {
		printErr(file, image.Line, fmt.Sprintf("image has invalid format '%s'", image.Value))
		os.Exit(1)
	}

	portsNode := findFieldValue(node, "ports")
	if portsNode != nil {
		for _, port := range portsNode.Content {
			validatePort(file, port)
		}
	}

	readiness := findFieldValue(node, "readinessProbe")
	if readiness != nil {
		validateProbe(file, readiness, "readinessProbe")
	}

	liveness := findFieldValue(node, "livenessProbe")
	if liveness != nil {
		validateProbe(file, liveness, "livenessProbe")
	}

	resources := findFieldValue(node, "resources")
	if resources == nil {
		printErr(file, node.Line, "resources is required")
		os.Exit(1)
	}
	validateResources(file, resources)
}

// === Проверка подузлов ===

func validatePort(file string, node *yaml.Node) {
	portNode := findFieldValue(node, "containerPort")
	if portNode == nil {
		printErr(file, node.Line, "containerPort is required")
		os.Exit(1)
	}
	val, err := strconv.Atoi(portNode.Value)
	if err != nil {
		printErr(file, portNode.Line, "containerPort must be int")
		os.Exit(1)
	}
	if val <= 0 || val >= 65536 {
		printErr(file, portNode.Line, "containerPort value out of range")
		os.Exit(1)
	}

	protocol := findFieldValue(node, "protocol")
	if protocol != nil && protocol.Value != "TCP" && protocol.Value != "UDP" {
		printErr(file, protocol.Line, fmt.Sprintf("protocol has unsupported value '%s'", protocol.Value))
		os.Exit(1)
	}
}

func validateProbe(file string, node *yaml.Node, probeType string) {
	httpGet := findFieldValue(node, "httpGet")
	if httpGet == nil {
		printErr(file, node.Line, fmt.Sprintf("%s.httpGet is required", probeType))
		os.Exit(1)
	}

	path := findFieldValue(httpGet, "path")
	if path == nil {
		printErr(file, node.Line, fmt.Sprintf("%s.httpGet.path is required", probeType))
		os.Exit(1)
	}
	if !strings.HasPrefix(path.Value, "/") {
		printErr(file, path.Line, fmt.Sprintf("%s.httpGet.path has invalid format '%s'", probeType, path.Value))
		os.Exit(1)
	}

	port := findFieldValue(httpGet, "port")
	if port == nil {
		printErr(file, node.Line, fmt.Sprintf("%s.httpGet.port is required", probeType))
		os.Exit(1)
	}
	val, err := strconv.Atoi(port.Value)
	if err != nil {
		printErr(file, port.Line, fmt.Sprintf("%s.httpGet.port must be int", probeType))
		os.Exit(1)
	}
	if val <= 0 || val >= 65536 {
		printErr(file, port.Line, fmt.Sprintf("%s.httpGet.port value out of range", probeType))
		os.Exit(1)
	}
}

func validateResources(file string, node *yaml.Node) {
	limits := findFieldValue(node, "limits")
	requests := findFieldValue(node, "requests")

	if limits == nil && requests == nil {
		printErr(file, node.Line, "resources.limits or requests is required")
		os.Exit(1)
	}

	if limits != nil {
		checkResourceBlock(file, limits, "limits")
	}
	if requests != nil {
		checkResourceBlock(file, requests, "requests")
	}
}

func checkResourceBlock(file string, node *yaml.Node, block string) {
	cpu := findFieldValue(node, "cpu")
	mem := findFieldValue(node, "memory")
	if cpu != nil {
		if _, err := strconv.Atoi(cpu.Value); err != nil {
			printErr(file, cpu.Line, fmt.Sprintf("resources.%s.cpu must be int", block))
			os.Exit(1)
		}
	}
	if mem != nil && !regexp.MustCompile(`^[0-9]+(Gi|Mi|Ki)$`).MatchString(mem.Value) {
		printErr(file, mem.Line, fmt.Sprintf("resources.%s.memory has invalid format '%s'", block, mem.Value))
		os.Exit(1)
	}
}

// === Вспомогательные функции ===

func findFieldValue(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Value == key {
			return v
		}
	}
	return nil
}

func printErr(file string, line int, msg string) {
	fmt.Fprintf(os.Stderr, "%s:%d %s\n", filepath.Base(file), line, msg)
}

func isUTF8(b []byte) bool {
	i := 0
	for i < len(b) {
		if b[i] <= 0x7F {
			i++
			continue
		} else if b[i] >= 0xC2 && b[i] <= 0xDF && i+1 < len(b) &&
			b[i+1] >= 0x80 && b[i+1] <= 0xBF {
			i += 2
			continue
		} else if b[i] >= 0xE0 && b[i] <= 0xEF && i+2 < len(b) &&
			b[i+1] >= 0x80 && b[i+1] <= 0xBF && b[i+2] >= 0x80 && b[i+2] <= 0xBF {
			i += 3
			continue
		} else if b[i] >= 0xF0 && b[i] <= 0xF4 && i+3 < len(b) &&
			b[i+1] >= 0x80 && b[i+1] <= 0xBF && b[i+2] >= 0x80 && b[i+2] <= 0xBF &&
			b[i+3] >= 0x80 && b[i+3] <= 0xBF {
			i += 4
			continue
		} else {
			return false
		}
	}
	return true
}
