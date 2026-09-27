// Package input reads what a pipeline is about to apply — rendered Kubernetes manifests (YAML or
// JSON) or a Terraform plan — and turns it into a change.Set. It reads only identity, action and
// references; it never validates syntax or schema, and never reads Secret data.
package input

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/miropshq/mirops-cli/internal/change"
)

// Read reads path: a file, a directory of .yaml/.yml/.json files, or "-" for stdin.
func Read(path string, stdin io.Reader) (*change.Set, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return parse(data, "stdin")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if err := refuseSource(path); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return parse(data, path)
	}
	return readDir(path)
}

func readDir(dir string) (*change.Set, error) {
	var files []string
	sawTerraform := false
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml", ".json":
			if err := refuseSource(p); err != nil {
				return err
			}
			files = append(files, p)
		case ".tf":
			sawTerraform = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		if sawTerraform {
			return nil, errTerraformSource
		}
		return nil, fmt.Errorf("%s has no .yaml, .yml or .json files", dir)
	}
	sort.Strings(files)
	out := &change.Set{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		s, err := parse(data, f)
		if err != nil {
			return nil, err
		}
		merge(out, s)
	}
	return out, nil
}

var errTerraformSource = errors.New("Terraform source (.tf) isn't read directly — only a plan knows what will change. " +
	"Run: terraform plan -out plan.out && terraform show -json plan.out | mirops scan -f -")

// refuseSource rejects files that are sources to render, not what will be applied.
func refuseSource(p string) error {
	base := strings.ToLower(filepath.Base(p))
	switch {
	case strings.HasSuffix(base, ".tf"):
		return errTerraformSource
	case base == "kustomization.yaml" || base == "kustomization.yml":
		return fmt.Errorf("%s is a Kustomize source — render it first: kustomize build <dir> | mirops scan -f -", p)
	case base == "chart.yaml" || base == "values.yaml" || base == "values.yml":
		return fmt.Errorf("%s is a Helm chart source — render it first: helm template <release> <chart> | mirops scan -f -", p)
	}
	return nil
}

func merge(into, s *change.Set) {
	if into.Format == "" {
		into.Format = s.Format
	} else if into.Format != s.Format {
		into.Format = "mixed"
	}
	into.Changes = append(into.Changes, s.Changes...)
	into.NonKubernetes += s.NonKubernetes
	into.NoOp += s.NoOp
	into.NotEvaluable = append(into.NotEvaluable, s.NotEvaluable...)
}

// parse detects the format of one blob and reads it.
func parse(data []byte, source string) (*change.Set, error) {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0:
		return nil, fmt.Errorf("%s is empty", source)
	case bytes.HasPrefix(trimmed, []byte("PK\x03\x04")):
		return nil, fmt.Errorf("%s is a binary Terraform plan — convert it: terraform show -json plan.out | mirops scan -f -", source)
	case trimmed[0] == '{' || trimmed[0] == '[':
		return parseJSON(trimmed, source)
	case bytes.Contains(trimmed, []byte("{{")):
		return nil, fmt.Errorf("%s contains template expressions ({{ … }}) — render it first, e.g. helm template <release> <chart> | mirops scan -f -", source)
	}
	return parseYAML(trimmed, source)
}

func parseJSON(data []byte, source string) (*change.Set, error) {
	var head struct {
		FormatVersion   string          `json:"format_version"`
		ResourceChanges json.RawMessage `json:"resource_changes"`
	}
	if json.Unmarshal(data, &head) == nil && head.FormatVersion != "" {
		return parseTerraformPlan(data, source)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", source, err)
	}
	set := &change.Set{Format: "json"}
	if err := addObjects(set, doc, source); err != nil {
		return nil, err
	}
	return set, nil
}

func parseYAML(data []byte, source string) (*change.Set, error) {
	set := &change.Set{Format: "yaml"}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for i := 1; ; i++ {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: document %d isn't valid YAML: %w", source, i, err)
		}
		if doc == nil {
			continue // an empty document between separators
		}
		if err := addObjects(set, doc, fmt.Sprintf("%s#%d", source, i)); err != nil {
			return nil, err
		}
	}
	if len(set.Changes) == 0 {
		return nil, fmt.Errorf("%s has no Kubernetes objects", source)
	}
	return set, nil
}

// addObjects adds a Kubernetes object, a List, or a JSON array of objects.
func addObjects(set *change.Set, doc any, source string) error {
	if list, ok := doc.([]any); ok {
		for _, o := range list {
			if err := addObjects(set, o, source); err != nil {
				return err
			}
		}
		return nil
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return fmt.Errorf("%s isn't a Kubernetes object", source)
	}
	kind, _ := obj["kind"].(string)
	apiVersion, _ := obj["apiVersion"].(string)
	if kind == "" || apiVersion == "" {
		return fmt.Errorf("%s isn't a Kubernetes manifest (no apiVersion/kind) — if it's a Helm values file, render the chart first: helm template <release> <chart> | mirops scan -f -", source)
	}
	if kind == "Kustomization" {
		return fmt.Errorf("%s is a Kustomize source — render it first: kustomize build <dir> | mirops scan -f -", source)
	}
	if strings.HasSuffix(kind, "List") {
		items, _ := obj["items"].([]any)
		return addObjects(set, items, source)
	}
	name, ns := change.Identity(obj)
	mk := change.MirrorKind(kind)
	set.Changes = append(set.Changes, change.Change{
		Kind: mk, Namespace: ns, Name: name, Action: change.Apply,
		Refs: change.Refs(mk, obj), Source: source,
	})
	return nil
}

// --- Terraform ------------------------------------------------------------------------------------

type tfPlan struct {
	ResourceChanges []struct {
		Address string `json:"address"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Change  struct {
			Actions []string `json:"actions"`
			Before  any      `json:"before"`
			After   any      `json:"after"`
		} `json:"change"`
	} `json:"resource_changes"`
}

// parseTerraformPlan reads `terraform show -json` output. Only Kubernetes resources that change are
// kept; everything else is counted for the summary line.
func parseTerraformPlan(data []byte, source string) (*change.Set, error) {
	var plan tfPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("%s: invalid Terraform plan JSON: %w", source, err)
	}
	set := &change.Set{Format: "terraform-plan"}
	for _, rc := range plan.ResourceChanges {
		if rc.Mode == "data" {
			continue
		}
		isManifest := rc.Type == "kubernetes_manifest" || rc.Type == "kubectl_manifest"
		typedKind := change.TerraformKind(rc.Type)
		switch {
		case rc.Type == "helm_release":
			set.NotEvaluable = append(set.NotEvaluable, change.Note{What: rc.Address,
				Reason: "a helm_release carries no manifests — check the chart with: helm template <release> <chart> | mirops scan -f -"})
			continue
		case !isManifest && typedKind == "" && strings.HasPrefix(rc.Type, "kubernetes_"):
			set.NotEvaluable = append(set.NotEvaluable, change.Note{What: rc.Address, Reason: rc.Type + " isn't modelled by the mirror"})
			continue
		case !isManifest && typedKind == "":
			set.NonKubernetes++
			continue
		}

		action, ok := tfAction(rc.Change.Actions)
		if !ok {
			set.NoOp++
			continue
		}
		before, after := rc.Change.Before, rc.Change.After
		if rc.Type == "kubernetes_manifest" {
			before, after = field(before, "manifest"), field(after, "manifest")
		} else if rc.Type == "kubectl_manifest" {
			before, after = yamlBody(before), yamlBody(after)
		}
		current := after
		if action == change.Delete {
			current = before
		}

		kind := typedKind
		if isManifest {
			k, _ := field(current, "kind").(string)
			kind = change.MirrorKind(k)
		}
		name, ns := change.Identity(current)
		if name == "" {
			set.NotEvaluable = append(set.NotEvaluable, change.Note{What: rc.Address, Reason: "its name is only known after apply"})
			continue
		}
		if !isManifest && ns == "" && !change.IsClusterScoped(kind) {
			ns = "default" // the kubernetes provider's default for typed resources
		}
		c := change.Change{
			Kind: kind, Namespace: ns, Name: name, Action: action,
			Refs: change.Refs(kind, current), Source: rc.Address,
		}
		if (action == change.Update || action == change.Replace) && before != nil {
			c.PrevRefs, c.HasPrev = change.Refs(kind, before), true
		}
		set.Changes = append(set.Changes, c)
	}
	return set, nil
}

func tfAction(actions []string) (change.Action, bool) {
	switch strings.Join(actions, ",") {
	case "create":
		return change.Create, true
	case "update":
		return change.Update, true
	case "delete":
		return change.Delete, true
	case "delete,create", "create,delete":
		return change.Replace, true
	}
	return "", false // no-op, read
}

func field(obj any, key string) any {
	m, _ := obj.(map[string]any)
	return m[key]
}

// yamlBody parses a kubectl_manifest's yaml_body into an object.
func yamlBody(v any) any {
	body, _ := field(v, "yaml_body").(string)
	if body == "" {
		return nil
	}
	var obj any
	if yaml.Unmarshal([]byte(body), &obj) != nil {
		return nil
	}
	return obj
}
