// Package change models what a deploy is about to do to the cluster: which objects it creates,
// updates or deletes, and which other objects each one references. It is filled from rendered
// manifests or a Terraform plan (package input) and judged against the mirror (package impact).
package change

import "strings"

// Action is what the change does to an object.
type Action string

const (
	// Apply is a manifest applied as-is (kubectl/helm/kustomize output): create or update, decided
	// later against the mirror.
	Apply   Action = "apply"
	Create  Action = "create"
	Update  Action = "update"
	Delete  Action = "delete"
	Replace Action = "replace"
)

// Ref is a reference from one object to another in the same namespace.
type Ref struct {
	Kind string `json:"kind"` // ConfigMap | Secret | PVC | Service
	Name string `json:"name"`
}

// Change is one object the deploy touches.
type Change struct {
	Kind      string `json:"kind"` // mirror kind: Deployment, Service, PVC, ConfigMap, ...
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Action    Action `json:"action"`
	// Refs are the object's references after the change (for a delete: before it).
	Refs []Ref `json:"refs,omitempty"`
	// PrevRefs are its references before the change, when the input says so (a Terraform update).
	// When HasPrev is false the mirror's current edges stand in for them.
	PrevRefs []Ref  `json:"-"`
	HasPrev  bool   `json:"-"`
	Source   string `json:"source,omitempty"` // file, or Terraform resource address
}

// ID is the object's id in the mirror graph.
func (c Change) ID() string { return ID(c.Kind, c.Namespace, c.Name) }

// ID builds a mirror graph id the way the operator does: "Kind/namespace/name", or "Kind/name" for
// cluster-scoped components.
func ID(kind, namespace, name string) string {
	if namespace == "" {
		return kind + "/" + name
	}
	return kind + "/" + namespace + "/" + name
}

// Note is something the input contained that isn't evaluated, and why.
type Note struct {
	What   string `json:"what"`
	Reason string `json:"reason"`
}

// Set is everything read from the input.
type Set struct {
	Format  string   `json:"format"` // yaml | json | terraform-plan
	Changes []Change `json:"changes"`
	// Counts of what was filtered out before evaluation, for the summary line.
	NonKubernetes int    `json:"nonKubernetes,omitempty"`
	NoOp          int    `json:"noOp,omitempty"`
	NotEvaluable  []Note `json:"notEvaluable,omitempty"`
}

// MirrorKind maps a Kubernetes kind to the kind the mirror graph uses for it.
func MirrorKind(k8sKind string) string {
	if k8sKind == "PersistentVolumeClaim" {
		return "PVC"
	}
	return k8sKind
}

// clusterScoped are kinds with no namespace; the mirror doesn't model them, so they are counted and
// skipped.
var clusterScoped = map[string]bool{
	"Namespace": true, "Node": true, "PersistentVolume": true, "StorageClass": true,
	"ClusterRole": true, "ClusterRoleBinding": true, "CustomResourceDefinition": true,
	"PriorityClass": true, "IngressClass": true, "RuntimeClass": true, "APIService": true,
	"MutatingWebhookConfiguration": true, "ValidatingWebhookConfiguration": true,
	"ValidatingAdmissionPolicy": true, "ValidatingAdmissionPolicyBinding": true,
	"CSIDriver": true, "VolumeAttachment": true,
}

// IsClusterScoped reports whether a kind has no namespace.
func IsClusterScoped(kind string) bool { return clusterScoped[kind] }

// --- reference extraction -----------------------------------------------------------------------

// The extractors read both shapes an object arrives in: Kubernetes manifests (camelCase, nested
// objects) and Terraform's typed kubernetes_* resources (snake_case, every nested block a list).
// field() accepts either key and unwraps one-element block lists, so one walker serves both.

// field returns obj[key] for the first key present, unwrapping a one-element list (a Terraform block).
func field(obj any, keys ...string) any {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if l, ok := v.([]any); ok && len(l) == 1 {
				if _, isObj := l[0].(map[string]any); isObj {
					return l[0]
				}
			}
			return v
		}
	}
	return nil
}

// items returns obj[key] as a list, for repeated fields (containers, volumes, rules...).
func items(obj any, keys ...string) []any {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range keys {
		switch v := m[k].(type) {
		case []any:
			return v
		case map[string]any:
			return []any{v}
		}
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// PodSpec finds the pod spec inside a workload of the given (mirror) kind, or nil.
func PodSpec(kind string, obj any) any {
	spec := field(obj, "spec")
	switch kind {
	case "Pod":
		return spec
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		return field(field(spec, "template"), "spec")
	case "CronJob":
		return field(field(field(field(spec, "jobTemplate", "job_template"), "spec"), "template"), "spec")
	}
	return nil
}

// PodSpecRefs returns the ConfigMaps, Secrets and PVCs a pod spec references — the same fields the
// operator reads to draw its edges (containers' envFrom and env valueFrom, and volumes), so a
// reference that already exists in the mirror compares equal.
func PodSpecRefs(spec any) []Ref {
	var refs []Ref
	seen := map[string]bool{}
	add := func(kind, name string) {
		if name == "" || seen[kind+"/"+name] {
			return
		}
		seen[kind+"/"+name] = true
		refs = append(refs, Ref{Kind: kind, Name: name})
	}
	for _, c := range items(spec, "containers", "container") {
		for _, ef := range items(c, "envFrom", "env_from") {
			add("ConfigMap", str(field(field(ef, "configMapRef", "config_map_ref"), "name")))
			add("Secret", str(field(field(ef, "secretRef", "secret_ref"), "name")))
		}
		for _, e := range items(c, "env") {
			vf := field(e, "valueFrom", "value_from")
			add("ConfigMap", str(field(field(vf, "configMapKeyRef", "config_map_key_ref"), "name")))
			add("Secret", str(field(field(vf, "secretKeyRef", "secret_key_ref"), "name")))
		}
	}
	for _, v := range items(spec, "volumes", "volume") {
		add("ConfigMap", str(field(field(v, "configMap", "config_map"), "name")))
		add("Secret", str(field(field(v, "secret"), "secretName", "secret_name")))
		add("PVC", str(field(field(v, "persistentVolumeClaim", "persistent_volume_claim"), "claimName", "claim_name")))
	}
	return refs
}

// IngressRefs returns the Services an Ingress routes to (rules and the default backend).
func IngressRefs(obj any) []Ref {
	var refs []Ref
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		refs = append(refs, Ref{Kind: "Service", Name: name})
	}
	spec := field(obj, "spec")
	for _, r := range items(spec, "rules", "rule") {
		for _, p := range items(field(r, "http"), "paths", "path") {
			add(str(field(field(field(p, "backend"), "service"), "name")))
		}
	}
	add(str(field(field(field(spec, "defaultBackend", "default_backend"), "service"), "name")))
	return refs
}

// Refs returns every reference an object of the given (mirror) kind makes.
func Refs(kind string, obj any) []Ref {
	if kind == "Ingress" {
		return IngressRefs(obj)
	}
	if spec := PodSpec(kind, obj); spec != nil {
		return PodSpecRefs(spec)
	}
	return nil
}

// Identity returns an object's name and namespace from its metadata (either shape).
func Identity(obj any) (name, namespace string) {
	md := field(obj, "metadata")
	return str(field(md, "name")), str(field(md, "namespace"))
}

// TerraformKinds maps Terraform kubernetes provider resource types to Kubernetes kinds. Both the
// versioned (_v1) and the legacy names are accepted.
var TerraformKinds = map[string]string{
	"deployment": "Deployment", "stateful_set": "StatefulSet", "daemon_set": "DaemonSet", "daemonset": "DaemonSet",
	"job": "Job", "cron_job": "CronJob", "pod": "Pod", "replication_controller": "ReplicationController",
	"config_map": "ConfigMap", "secret": "Secret", "service": "Service", "ingress": "Ingress",
	"persistent_volume_claim": "PVC", "namespace": "Namespace", "service_account": "ServiceAccount",
}

// TerraformKind returns the kind for a kubernetes_* resource type, or "" when it isn't one of them.
func TerraformKind(resourceType string) string {
	t := strings.TrimPrefix(resourceType, "kubernetes_")
	if t == resourceType {
		return ""
	}
	for _, suffix := range []string{"_v2", "_v1"} {
		t = strings.TrimSuffix(t, suffix)
	}
	return TerraformKinds[t]
}
