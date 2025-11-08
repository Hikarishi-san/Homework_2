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

func validateYAML(root *yaml.Node, filename string) []string {
	var errs []string
	if len(root.Content) == 0 {
		return []string{fmt.Sprintf("%s: empty file", filename)}
	}
	doc := root.Content[0]
	fields := mapify(doc)
	if apiVersion, ok := fields["apiVersion"]; ok {
		if apiVersion.Value != "v1" {
			errs = append(errs, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", filename, apiVersion.Line, apiVersion.Value))
		}
	} else {
		errs = append(errs, "apiVersion is required")
	}
	if kind, ok := fields["kind"]; ok {
		if kind.Value != "Pod" {
			errs = append(errs, fmt.Sprintf("%s:%d kind has unsupported value '%s'", filename, kind.Line, kind.Value))
		}
	} else {
		errs = append(errs, "kind is required")
	}
	if meta, ok := fields["metadata"]; ok {
		errs = append(errs, validateMetadata(meta, filename)...)
	} else {
		errs = append(errs, "metadata is required")
	}
	if spec, ok := fields["spec"]; ok {
		errs = append(errs, validateSpec(spec, filename)...)
	} else {
		errs = append(errs, "spec is required")
	}
	return errs
}

func validateMetadata(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "name is required")
	} else if nameNode.Value == "" {
		errs = append(errs, fmt.Sprintf("%s:%d name is required", filename, nameNode.Line))
	}
	if ns, ok := fields["namespace"]; ok && ns.Tag != "!!str" {
		errs = append(errs, fmt.Sprintf("%s:%d namespace must be string", filename, ns.Line))
	}
	return errs
}

func validateSpec(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)
	if osNode, ok := fields["os"]; ok {
		if osNode.Kind == yaml.ScalarNode {
			valid := map[string]bool{"linux": true, "windows": true}
			if !valid[osNode.Value] {
				errs = append(errs, fmt.Sprintf("%s:%d os has unsupported value '%s'", filename, osNode.Line, osNode.Value))
			}
		}
	}
	if containers, ok := fields["containers"]; ok {
		if containers.Kind == yaml.SequenceNode && len(containers.Content) > 0 {
			for _, c := range containers.Content {
				errs = append(errs, validateContainer(c, filename)...)
			}
		}
	}
	return errs
}

func validateContainer(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)
	nameNode, hasName := fields["name"]
	if !hasName {
		errs = append(errs, "name is required")
	} else if nameNode.Value == "" {
		errs = append(errs, fmt.Sprintf("%s:%d name is required", filename, nameNode.Line))
	}
	if imageNode, hasImage := fields["image"]; !hasImage {
		errs = append(errs, "image is required")
	} else if !isValidImageFormat(imageNode.Value) {
		errs = append(errs, fmt.Sprintf("%s:%d image has invalid format '%s'", filename, imageNode.Line, imageNode.Value))
	}
	if ports, ok := fields["ports"]; ok && ports.Kind == yaml.SequenceNode {
		for _, portNode := range ports.Content {
			if pf := mapify(portNode); len(pf) > 0 {
				if p, ok := pf["containerPort"]; ok {
					port, err := strconv.Atoi(p.Value)
					if err != nil || port <= 0 || port >= 65536 {
						errs = append(errs, fmt.Sprintf("%s:%d containerPort value out of range", filename, p.Line))
					}
				}
			}
		}
	}
	if probe, ok := fields["readinessProbe"]; ok {
		errs = append(errs, validateProbe(probe, filename)...)
	}
	if probe, ok := fields["livenessProbe"]; ok {
		errs = append(errs, validateProbe(probe, filename)...)
	}
	if res, ok := fields["resources"]; ok {
		resFields := mapify(res)
		if limits, ok := resFields["limits"]; ok {
			errs = append(errs, validateResourceMap(limits, filename)...)
		}
		if req, ok := resFields["requests"]; ok {
			errs = append(errs, validateResourceMap(req, filename)...)
		}
	}
	return errs
}

func isValidImageFormat(s string) bool {
	return strings.Contains(s, "registry.bigbrother.io/") && strings.Contains(s, ":")
}

func validateProbe(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)
	if httpGetNode, ok := fields["httpGet"]; ok {
		httpFields := mapify(httpGetNode)
		if pathNode, hasPath := httpFields["path"]; !hasPath {
			errs = append(errs, "path is required")
		} else if !strings.HasPrefix(pathNode.Value, "/") {
			errs = append(errs, fmt.Sprintf("%s:%d path has invalid format '%s'", filename, pathNode.Line, pathNode.Value))
		}
		if portNode, hasPort := httpFields["port"]; !hasPort {
			errs = append(errs, "port is required")
		} else {
			port, err := strconv.Atoi(portNode.Value)
			if err != nil || port <= 0 || port >= 65536 {
				errs = append(errs, fmt.Sprintf("%s:%d port value out of range", filename, portNode.Line))
			}
		}
	}
	return errs
}

func validateResourceMap(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)
	if cpu, ok := fields["cpu"]; ok {
		if cpu.Tag != "!!int" {
			errs = append(errs, fmt.Sprintf("%s:%d cpu must be int", filename, cpu.Line))
		}
	}
	if memory, ok := fields["memory"]; ok {
		re := regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)
		if memory.Tag != "!!str" || !re.MatchString(memory.Value) {
			errs = append(errs, fmt.Sprintf("%s:%d memory has invalid format '%s'", filename, memory.Line, memory.Value))
		}
	}
	return errs
}
