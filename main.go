package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yamlvalidator <file>")
		os.Exit(1)
	}

	filename := os.Args[1]
	data, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	errs := validateYAML(&root, filename)

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		os.Exit(1)
	}
	os.Exit(0)
}

// --- VALIDATION LOGIC ---

func validateYAML(root *yaml.Node, filename string) []string {
	var errs []string
	if len(root.Content) == 0 {
		return []string{fmt.Sprintf("%s: empty file", filename)}
	}

	doc := root.Content[0]
	fields := mapify(doc)

	// Проверяем обязательные поля верхнего уровня
	errs = append(errs, validateTopLevel(fields, doc, filename)...)

	// metadata
	if meta, ok := fields["metadata"]; ok {
		errs = append(errs, validateMetadata(meta, filename)...)
	} else {
		errs = append(errs, "metadata is required")
	}

	// spec
	if spec, ok := fields["spec"]; ok {
		errs = append(errs, validateSpec(spec, filename)...)
	} else {
		errs = append(errs, "spec is required")
	}

	return errs
}

// Проверка полей верхнего уровня
func validateTopLevel(fields map[string]*yaml.Node, node *yaml.Node, filename string) []string {
	var errs []string

	// apiVersion
	if apiVersion, ok := fields["apiVersion"]; ok {
		if apiVersion.Value != "v1" {
			errs = append(errs, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", filename, apiVersion.Line, apiVersion.Value))
		}
	} else {
		errs = append(errs, "apiVersion is required")
	}

	// kind
	if kind, ok := fields["kind"]; ok {
		if kind.Value != "Pod" {
			errs = append(errs, fmt.Sprintf("%s:%d kind has unsupported value '%s'", filename, kind.Line, kind.Value))
		}
	} else {
		errs = append(errs, "kind is required")
	}

	return errs
}

func mapify(node *yaml.Node) map[string]*yaml.Node {
	m := make(map[string]*yaml.Node)
	if node.Kind != yaml.MappingNode {
		return m
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		m[node.Content[i].Value] = node.Content[i+1]
	}
	return m
}

// --- METADATA ---

func validateMetadata(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// name - обязательное
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "metadata.name is required")
	} else if nameNode.Value == "" {
		errs = append(errs, fmt.Sprintf("%s:%d metadata.name is required", filename, nameNode.Line))
	}

	// namespace - опционально, но если есть - должно быть string
	if ns, ok := fields["namespace"]; ok && ns.Tag != "!!str" {
		errs = append(errs, fmt.Sprintf("%s:%d namespace must be string", filename, ns.Line))
	}

	return errs
}

// --- SPEC ---

func validateSpec(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// os - опционально
	if osNode, ok := fields["os"]; ok {
		errs = append(errs, validatePodOS(osNode, filename)...)
	}

	// containers - обязательно
	if containers, ok := fields["containers"]; ok {
		if containers.Kind != yaml.SequenceNode {
			errs = append(errs, fmt.Sprintf("%s:%d containers must be a list", filename, containers.Line))
		} else if len(containers.Content) == 0 {
			errs = append(errs, fmt.Sprintf("%s:%d containers cannot be empty", filename, containers.Line))
		} else {
			for _, c := range containers.Content {
				errs = append(errs, validateContainer(c, filename)...)
			}
		}
	} else {
		errs = append(errs, "spec.containers is required")
	}

	return errs
}

// Валидация PodOS
func validatePodOS(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// os.name - обязательное поле в os
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "spec.os.name is required")
	} else {
		valid := map[string]bool{"linux": true, "windows": true}
		if !valid[nameNode.Value] {
			errs = append(errs, fmt.Sprintf("%s:%d os has unsupported value '%s'", filename, nameNode.Line, nameNode.Value))
		}
	}

	return errs
}

func validateContainer(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// name - обязательно
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "containers[].name is required")
	} else if nameNode.Value == "" {
		errs = append(errs, fmt.Sprintf("%s:%d containers[].name is required", filename, nameNode.Line))
	} else if !isSnakeCase(nameNode.Value) {
		errs = append(errs, fmt.Sprintf("%s:%d containers[].name has invalid format '%s'", filename, nameNode.Line, nameNode.Value))
	}

	// image - обязательно
	if imageNode, hasImage := fields["image"]; !hasImage {
		errs = append(errs, "containers[].image is required")
	} else if !isValidImageFormat(imageNode.Value) {
		errs = append(errs, fmt.Sprintf("%s:%d image has invalid format '%s'", filename, imageNode.Line, imageNode.Value))
	}

	// ports - опционально
	if ports, ok := fields["ports"]; ok && ports.Kind == yaml.SequenceNode {
		for _, portNode := range ports.Content {
			errs = append(errs, validatePort(portNode, filename)...)
		}
	}

	// readinessProbe - опционально
	if probe, ok := fields["readinessProbe"]; ok {
		errs = append(errs, validateProbe(probe, "readinessProbe", filename)...)
	}

	// livenessProbe - опционально
	if probe, ok := fields["livenessProbe"]; ok {
		errs = append(errs, validateProbe(probe, "livenessProbe", filename)...)
	}

	// resources - обязательно
	if res, ok := fields["resources"]; ok {
		resFields := mapify(res)
		if limits, ok := resFields["limits"]; ok {
			errs = append(errs, validateResourceMap(limits, "limits", filename)...)
		}
		if req, ok := resFields["requests"]; ok {
			errs = append(errs, validateResourceMap(req, "requests", filename)...)
		}
	} else {
		errs = append(errs, "containers[].resources is required")
	}

	return errs
}

// Валидация snake_case
func isSnakeCase(s string) bool {
	// Может содержать буквы, цифры и подчеркивания
	// Должно начинаться с буквы или цифры
	if len(s) == 0 {
		return false
	}
	matched, _ := regexp.MatchString(`^[a-z0-9_]+$`, s)
	return matched
}

// Валидация формата image
func isValidImageFormat(s string) bool {
	// Должен содержать registry.bigbrother.io и иметь тег версии (начинается с :)
	if !strings.Contains(s, "registry.bigbrother.io/") {
		return false
	}
	if !strings.Contains(s, ":") {
		return false
	}
	return true
}

// Валидация Probe
func validateProbe(node *yaml.Node, probeName string, filename string) []string {
	var errs []string
	fields := mapify(node)

	// httpGet - обязательно в пробе
	if httpGetNode, ok := fields["httpGet"]; ok {
		errs = append(errs, validateHTTPGetAction(httpGetNode, probeName, filename)...)
	} else {
		errs = append(errs, fmt.Sprintf("%s.httpGet is required", probeName))
	}

	return errs
}

// Валидация HTTPGetAction
func validateHTTPGetAction(node *yaml.Node, probeName string, filename string) []string {
	var errs []string
	fields := mapify(node)

	// path - обязательно
	if pathNode, hasPath := fields["path"]; !hasPath {
		errs = append(errs, fmt.Sprintf("%s.httpGet.path is required", probeName))
	} else if !strings.HasPrefix(pathNode.Value, "/") {
		errs = append(errs, fmt.Sprintf("%s:%d %s.httpGet.path has invalid format '%s'", filename, pathNode.Line, probeName, pathNode.Value))
	}

	// port - обязательно
	if portNode, hasPort := fields["port"]; !hasPort {
		errs = append(errs, fmt.Sprintf("%s.httpGet.port is required", probeName))
	} else {
		port, err := strconv.Atoi(portNode.Value)
		if err != nil || port <= 0 || port >= 65536 {
			errs = append(errs, fmt.Sprintf("%s:%d %s.httpGet.port value out of range", filename, portNode.Line, probeName))
		}
	}

	return errs
}

// --- PORTS ---

func validatePort(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// containerPort - обязательно
	if p, ok := fields["containerPort"]; ok {
		port, err := strconv.Atoi(p.Value)
		if err != nil || port <= 0 || port >= 65536 {
			errs = append(errs, fmt.Sprintf("%s:%d containerPort value out of range", filename, p.Line))
		}
	} else {
		errs = append(errs, "ports[].containerPort is required")
	}

	// protocol - опционально, но если есть - должно быть TCP или UDP
	if protNode, ok := fields["protocol"]; ok {
		valid := map[string]bool{"TCP": true, "UDP": true}
		if !valid[protNode.Value] {
			errs = append(errs, fmt.Sprintf("%s:%d protocol has unsupported value '%s'", filename, protNode.Line, protNode.Value))
		}
	}

	return errs
}

// --- RESOURCES ---

func validateResourceMap(node *yaml.Node, resType string, filename string) []string {
	var errs []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]

		switch key {
		case "cpu":
			if val.Tag != "!!int" {
				errs = append(errs, fmt.Sprintf("%s:%d cpu must be int", filename, val.Line))
			}
		case "memory":
			re := regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)
			if val.Tag != "!!str" || !re.MatchString(val.Value) {
				errs = append(errs, fmt.Sprintf("%s:%d memory has invalid format '%s'", filename, val.Line, val.Value))
			}
		}
	}
	return errs
}
