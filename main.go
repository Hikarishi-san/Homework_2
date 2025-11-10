package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"

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

	// metadata
	if meta, ok := fields["metadata"]; ok {
		errs = append(errs, validateMetadata(meta, filename)...)
	} else {
		errs = append(errs, fmt.Sprintf("%s:%d metadata section missing", filename, doc.Line))
	}

	// spec
	if spec, ok := fields["spec"]; ok {
		errs = append(errs, validateSpec(spec, filename)...)
	} else {
		errs = append(errs, fmt.Sprintf("%s:%d spec section missing", filename, doc.Line))
	}

	return errs
}

func mapify(node *yaml.Node) map[string]*yaml.Node {
	m := make(map[string]*yaml.Node)
	for i := 0; i+1 < len(node.Content); i += 2 {
		m[node.Content[i].Value] = node.Content[i+1]
	}
	return m
}

// --- METADATA ---

func validateMetadata(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	nameNode, hasName := fields["name"]
	if !hasName || nameNode.Value == "" {
		line := node.Line
		if hasName {
			line = nameNode.Line
		}
		errs = append(errs, fmt.Sprintf("%s:%d name is required", filename, line))
	}

	if ns, ok := fields["namespace"]; ok && ns.Tag != "!!str" {
		errs = append(errs, fmt.Sprintf("%s:%d namespace must be string", filename, ns.Line))
	}

	return errs
}

// --- SPEC ---

func validateSpec(node *yaml.Node, filename string) []string {
 var errs []string
 fields := mapify(node)

 if osNode, ok := fields["os"]; ok {
  if osNode.Tag != "!!str" || (osNode.Value != "linux" && osNode.Value != "windows") {
   errs = append(errs, fmt.Sprintf("%s:%d os has unsupported value '%s'", filename, osNode.Line, osNode.Value))
  }
 } else {
  errs = append(errs, fmt.Sprintf("%s:%d os is required", filename, node.Line))
 }

 if containers, ok := fields["containers"]; ok && containers.Kind == yaml.SequenceNode {
  for _, c := range containers.Content {
   errs = append(errs, validateContainer(c, filename)...)
  }
 }

 return errs
}

func validateContainer(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	nameNode, hasName := fields["name"]
	if !hasName || nameNode.Value == "" {
		line := node.Line
		if hasName {
			line = nameNode.Line
		}
		errs = append(errs, fmt.Sprintf("%s:%d name is required", filename, line))
	}

	if ports, ok := fields["ports"]; ok && ports.Kind == yaml.SequenceNode {
		for _, portNode := range ports.Content {
			errs = append(errs, validatePort(portNode, filename)...)
		}
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

// --- PORTS ---

func validatePort(node *yaml.Node, filename string) []string {
	var errs []string
	fields := mapify(node)

	if p, ok := fields["containerPort"]; ok {
		port, err := strconv.Atoi(p.Value)
		if err != nil || port <= 0 || port >= 65536 {
			errs = append(errs, fmt.Sprintf("%s:%d containerPort value out of range", filename, p.Line))
		}
	}

	return errs
}

// --- RESOURCES ---

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
