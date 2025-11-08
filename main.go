package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

type Pod struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   ObjectMeta `yaml:"metadata"`
	Spec       PodSpec    `yaml:"spec"`
}

type ObjectMeta struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"`
	Labels    map[string]string `yaml:"labels"`
}

type PodSpec struct {
	OS         *PodOS      `yaml:"os"`
	Containers []Container `yaml:"containers"`
}

type PodOS struct {
	Name string `yaml:"name"`
}

type Container struct {
	Name           string              `yaml:"name"`
	Image          string              `yaml:"image"`
	Ports          PortList            `yaml:"ports"`
	ReadinessProbe *Probe              `yaml:"readinessProbe"`
	LivenessProbe  *Probe              `yaml:"livenessProbe"`
	Resources      ResourceRequirements `yaml:"resources"`
}

type ContainerPort struct {
	ContainerPort int    `yaml:"containerPort"`
	Protocol      string `yaml:"protocol"`
}

// PortList supports either a single object or a list in YAML.
type PortList []ContainerPort

func (pl *PortList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		var ports []ContainerPort
		if err := value.Decode(&ports); err != nil {
			return err
		}
		*pl = ports
		return nil
	case yaml.MappingNode:
		var p ContainerPort
		if err := value.Decode(&p); err != nil {
			return err
		}
		*pl = []ContainerPort{p}
		return nil
	case yaml.ScalarNode, yaml.AliasNode:
		return fmt.Errorf("ports: unsupported YAML node kind for ports")
	default:
		return fmt.Errorf("ports: invalid YAML structure")
	}
}

type Probe struct {
	HTTPGet *HTTPGetAction `yaml:"httpGet"`
}

type HTTPGetAction struct {
	Path string `yaml:"path"`
	Port int    `yaml:"port"`
}

type ResourceRequirements struct {
	Requests map[string]interface{} `yaml:"requests"`
	Limits   map[string]interface{} `yaml:"limits"`
}

type validationErrors struct {
	errs []string
}

func (v *validationErrors) add(msg string) {
	v.errs = append(v.errs, msg)
}

func (v *validationErrors) addf(format string, args ...any) {
	v.errs = append(v.errs, fmt.Sprintf(format, args...))
}

func (v *validationErrors) has() bool {
	return len(v.errs) > 0
}

func (v *validationErrors) Error() string {
	if !v.has() {
		return ""
	}
	sb := strings.Builder{}
	sb.WriteString("Validation failed:\n")
	for _, e := range v.errs {
		sb.WriteString(" - ")
		sb.WriteString(e)
		sb.WriteString("\n")
	}
	return sb.String()
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s <path-to-yaml>\n", path.Base(os.Args[0]))
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	filename := flag.Arg(0)

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read error: %v\n", err)
		os.Exit(1)
	}

	var pod Pod
	if err := yaml.Unmarshal(data, &pod); err != nil {
		fmt.Fprintf(os.Stderr, "YAML decode error: %v\n", err)
		os.Exit(1)
	}

	if err := validatePod(&pod); err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}

	fmt.Println("OK")
}

func validatePod(p *Pod) error {
	verr := &validationErrors{}

	// Top-level fields
	if p.APIVersion != "v1" {
		verr.addf("apiVersion must be \"v1\", got %q", p.APIVersion)
	}
	if p.Kind != "Pod" {
		verr.addf("kind must be \"Pod\", got %q", p.Kind)
	}

	// metadata
	if p.Metadata.Name == "" {
		verr.add("metadata.name is required and must be non-empty")
	}
	// namespace optional; labels optional (map[string]string already constrains values)

	// spec
	if len(p.Spec.Containers) == 0 {
		verr.add("spec.containers is required and must contain at least one container")
	}

	// spec.os
	if p.Spec.OS != nil {
		if !oneOf(p.Spec.OS.Name, "linux", "windows") {
			verr.addf("spec.os.name must be one of [linux, windows], got %q", p.Spec.OS.Name)
		}
	}

	// containers
	seenNames := map[string]struct{}{}
	for i := range p.Spec.Containers {
		c := &p.Spec.Containers[i]
		cpath := fmt.Sprintf("spec.containers[%d]", i)

		// name
		if c.Name == "" {
			verr.addf("%s.name is required and must be non-empty", cpath)
		} else {
			if _, ok := seenNames[c.Name]; ok {
				verr.addf("%s.name %q must be unique within the pod", cpath, c.Name)
			}
			seenNames[c.Name] = struct{}{}
			if !isSnakeCase(c.Name) {
				verr.addf("%s.name must be snake_case, got %q", cpath, c.Name)
			}
		}

		// image
		if c.Image == "" {
			verr.addf("%s.image is required", cpath)
		} else if !isValidImage(c.Image) {
			verr.addf("%s.image must be from domain registry.bigbrother.io and include a tag, got %q", cpath, c.Image)
		}

		// ports (optional)
		for j, port := range c.Ports {
			ppath := fmt.Sprintf("%s.ports[%d]", cpath, j)
			if !isValidPort(port.ContainerPort) {
				verr.addf("%s.containerPort must be in range 1..65535, got %d", ppath, port.ContainerPort)
			}
			// Default protocol to TCP if empty for validation purposes
			if port.Protocol != "" && !oneOf(port.Protocol, "TCP", "UDP") {
				verr.addf("%s.protocol must be one of [TCP, UDP], got %q", ppath, port.Protocol)
			}
		}

		// probes (optional but if present, must have httpGet with valid fields)
		if c.ReadinessProbe != nil {
			validateProbe(c.ReadinessProbe, verr, cpath+".readinessProbe")
		}
		if c.LivenessProbe != nil {
			validateProbe(c.LivenessProbe, verr, cpath+".livenessProbe")
		}

		// resources (required)
		validateResources(c.Resources, verr, cpath+".resources")
	}

	if verr.has() {
		return verr
	}
	return nil
}

func validateProbe(pr *Probe, verr *validationErrors, pfx string) {
	if pr.HTTPGet == nil {
		verr.addf("%s.httpGet is required", pfx)
		return
	}
	// path absolute
	if pr.HTTPGet.Path == "" {
		verr.addf("%s.httpGet.path is required and must be non-empty", pfx)
	} else if !isAbsolutePath(pr.HTTPGet.Path) {
		verr.addf("%s.httpGet.path must be an absolute path, got %q", pfx, pr.HTTPGet.Path)
	}
	// port
	if !isValidPort(pr.HTTPGet.Port) {
		verr.addf("%s.httpGet.port must be in range 1..65535, got %d", pfx, pr.HTTPGet.Port)
	}
}

func validateResources(rr ResourceRequirements, verr *validationErrors, pfx string) {
	// Both requests and limits are optional, but when present they must only contain known keys with valid formats.

	validateResMap := func(m map[string]interface{}, which string) {
		if m == nil {
			return
		}
		for k, v := range m {
			switch k {
			case "cpu":
				if !isIntLike(v) {
					verr.addf("%s.%s.cpu must be an integer number of cores", pfx, which)
				} else {
					// ensure integer and non-negative
					val, ok := toInt(v)
					if !ok || val < 0 {
						verr.addf("%s.%s.cpu must be a non-negative integer", pfx, which)
					}
				}
			case "memory":
				str, ok := toString(v)
				if !ok {
					verr.addf("%s.%s.memory must be a string like 512Mi, 2Gi, or 1024Ki", pfx, which)
					continue
				}
				if !isValidMemory(str) {
					verr.addf("%s.%s.memory must use binary units Ki, Mi, or Gi (e.g., 512Mi)", pfx, which)
				}
			default:
				verr.addf("%s.%s contains unsupported resource %q (allowed: cpu, memory)", pfx, which, k)
			}
		}
	}

	validateResMap(rr.Requests, "requests")
	validateResMap(rr.Limits, "limits")
}

func isSnakeCase(s string) bool {
	re := regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)
	return re.MatchString(s)
}

func isValidImage(img string) bool {
	// Must be registry.bigbrother.io/<path>:<tag>
	// Accepts repository paths with letters, digits, underscores, dots, dashes, and slashes.
	re := regexp.MustCompile(`^registry\.bigbrother\.io\/[A-Za-z0-9._\/-]+:[A-Za-z0-9._-]+$`)
	return re.MatchString(img)
}

func isValidPort(p int) bool {
	return p >= 1 && p <= 65535
}

func isAbsolutePath(p string) bool {
	return strings.HasPrefix(p, "/")
}

func oneOf[T comparable](val T, opts ...T) bool {
	for _, o := range opts {
		if val == o {
			return true
		}
	}
	return false
}

func isIntLike(v interface{}) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64:
		return true
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float32, float64:
		// YAML numbers typically decode into int, but guard anyway: only allow if integral
		f := toFloat(v)
		return float64(int64(f)) == f
	case string:
		_, err := parseIntString(v.(string))
		return err == nil
	default:
		return false
	}
}

func toInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int8:
		return int(t), true
	case int16:
		return int(t), true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case uint:
		return int(t), true
	case uint8:
		return int(t), true
	case uint16:
		return int(t), true
	case uint32:
		return int(t), true
	case uint64:
		if t > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(t), true
	case float32:
		f := float64(t)
		if float64(int64(f)) != f {
			return 0, false
		}
		return int(f), true
	case float64:
		if float64(int64(t)) != t {
			return 0, false
		}
		return int(t), true
	case string:
		n, err := parseIntString(t)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float32:
		return float64(t)
	case float64:
		return t
	case int:
		return float64(t)
	case int8:
		return float64(t)
	case int16:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint8:
		return float64(t)
	case uint16:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	default:
		return 0
	}
}

func toString(v interface{}) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", t), true
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", t), true
	case float32, float64:
		// only accept integral values for memory if mistakenly numeric
		f := toFloat(t)
		if float64(int64(f)) != f {
			return "", false
		}
		return fmt.Sprintf("%d", int64(f)), true
	default:
		return "", false
	}
}

func parseIntString(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	neg := false
	if s[0] == '+' || s[0] == '-' {
		if s[0] == '-' {
			neg = true
		}
		s = s[1:]
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-digit %q", r)
		}
	}
	// simple parse to avoid strconv import clutter
	var n int
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

func isValidMemory(s string) bool {
	// format: <digits>(Gi|Mi|Ki)
	re := regexp.MustCompile(`^[0-9]+(Gi|Mi|Ki)$`)
	return re.MatchString(s)
}

// optional helper to pretty print sorted errors (not used directly but can be handy)
func sortedErrors(err error) string {
	if err == nil {
		return ""
	}
	ve, ok := err.(*validationErrors)
	if !ok {
		return err.Error()
	}
	cp := make([]string, len(ve.errs))
	copy(cp, ve.errs)
	sort.Strings(cp)
	return "Validationion failed:\n - " + strings.Join(cp, "\n - ")
}
