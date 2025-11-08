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

func validateYAML(root *yaml.Node, filename string) []string {
	var errs []string
	if len(root.Content) == 0 {
		return []string{fmt.Sprintf("%s: empty file", filename)}
	}

	doc := root.Content[0]
	fields := mapify(doc)

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

func validateMetadata(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// name - обязательное
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "name is required")
	} else if nameNode.Value == "" {
		errs = append(errs, fmt.Sprintf("%s:%d name is required", filename, nameNode.Line))
	}

	// namespace - опционально
	if ns, ok := fields["namespace"]; ok && ns.Tag != "!!str" {
		errs = append(errs, fmt.Sprintf("%s:%d namespace must be string", filename, ns.Line))
	}

	return errs
}

func validateSpec(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// os - опционально, но если есть - проверяем значение
	if osNode, ok := fields["os"]; ok {
		if osNode.Kind == yaml.ScalarNode {
			// os это просто строка (как в примере)
			valid := map[string]bool{"linux": true, "windows": true}
			if !valid[osNode.Value] {
				errs = append(errs, fmt.Sprintf("%s:%d os has unsupported value '%s'", filename, osNode.Line, osNode.Value))
			}
		} else if osNode.Kind == yaml.MappingNode {
			// os это объект (с полем name)
			osFields := mapify(osNode)
			if nameNode, hasName := osFields["name"]; !hasName {
				errs = append(errs, "os.name is required")
			} else {
				valid := map[string]bool{"linux": true, "windows": true}
				if !valid[nameNode.Value] {
					errs = append(errs, fmt.Sprintf("%s:%d name has unsupported value '%s'", filename, nameNode.Line, nameNode.Value))
				}
			}
		}
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

func validateContainer(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// name - обязательно
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "containers[].name is required")
	} else if nameNode.Value == "" {
		errs = append(errs, fmt.Sprintf("%s:%d name is required", filename, nameNode.Line))
	} else if !isSnakeCase(nameNode.Value) {
		errs = append(errs, fmt.Sprintf("%s:%d name has invalid format '%s'", filename, nameNode.Line, nameNode.Value))
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
		errs = append(errs, validateProbe(probe, filename)...)
	}

	// livenessProbe - опционально
	if probe, ok := fields["livenessProbe"]; ok {
		errs = append(errs, validateProbe(probe, filename)...)
	}

	// resources - обязательно
	if res, ok := fields["resources"]; ok {
		resFields := mapify(res)
		if limits, ok := resFields["limits"]; ok {
			errs = append(errs, validateResourceMap(limits, filename)...)
		}
		if req, ok := resFields["requests"]; ok {
			errs = append(errs, validateResourceMap(req, filename)...)
		}
	} else {
		errs = append(errs, "containers[].resources is required")
	}

	return errs
}

func isSnakeCase(s string) bool {
	if len(s) == 0 {
		return false
	}
	matched, _ := regexp.MatchString(`^[a-z0-9_]+$`, s)
	return matched
}

func isValidImageFormat(s string) bool {
	if !strings.Contains(s, "registry.bigbrother.io/") {
		return false
	}
	if !strings.Contains(s, ":") {
		return false
	}
	return true
}

func validateProbe(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// httpGet - обязательно
	if httpGetNode, ok := fields["httpGet"]; ok {
		errs = append(errs, validateHTTPGetAction(httpGetNode, filename)...)
	} else {
		errs = append(errs, "httpGet is required")
	}

	return errs
}

func validateHTTPGetAction(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	// path - обязательно
	if pathNode, hasPath := fields["path"]; !hasPath {
		errs = append(errs, "path is required")
	} else if !strings.HasPrefix(pathNode.Value, "/") {
		errs = append(errs, fmt.Sprintf("%s:%d path has invalid format '%s'", filename, pathNode.Line, pathNode.Value))
	}

	// port - обязательно
	if portNode, hasPort := fields["port"]; !hasPort {
		errs = append(errs, "port is required")
	} else {
		port, err := strconv.Atoi(portNode.Value)
		if err != nil || port <= 0 || port >= 65536 {
			errs = append(errs, fmt.Sprintf("%s:%d port value out of range", filename, portNode.Line))
		}
	}

	return errs
}

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
		errs = append(errs, "containerPort is required")
	}

	// protocol - опционально
	if protNode, ok := fields["protocol"]; ok {
		valid := map[string]bool{"TCP": true, "UDP": true}
		if !valid[protNode.Value] {
			errs = append(errs, fmt.Sprintf("%s:%d protocol has unsupported value '%s'", filename, protNode.Line, protNode.Value))
		}
	}

	return errs
}

func validateResourceMap(node *yaml.Node, filename string) []string {
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