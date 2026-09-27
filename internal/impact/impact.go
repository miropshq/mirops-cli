// Package impact judges a change against the mirror: what the deploy would break, what it touches,
// and what the mirror report can't vouch for. It only reads the report's dependency graph — never the
// cluster — so it runs in any pipeline without credentials.
//
// The rule of thumb: block only on what the change itself introduces. A reference that was already
// broken before the change, or an object that is already down, is context — the deploy may be the fix.
package impact

import (
	"fmt"
	"sort"
	"strings"

	"github.com/miropshq/mirops-cli/internal/change"
	"github.com/miropshq/mirops-cli/internal/report"
)

// Finding is one line of the impact report.
type Finding struct {
	Object     string   `json:"object"` // "deployment/checkout"
	Namespace  string   `json:"namespace,omitempty"`
	Message    string   `json:"message"`
	Dependents []string `json:"dependents,omitempty"`
}

// Summary says what was read and what was left out, so nothing is silently skipped.
type Summary struct {
	Format          string        `json:"format"`
	Changes         int           `json:"changes"`
	Evaluated       int           `json:"evaluated"`
	OtherNamespaces int           `json:"otherNamespaces,omitempty"`
	ClusterScoped   int           `json:"clusterScoped,omitempty"`
	NoNamespace     int           `json:"noNamespace,omitempty"`
	NonKubernetes   int           `json:"nonKubernetes,omitempty"`
	NoOp            int           `json:"noOp,omitempty"`
	NotEvaluable    []change.Note `json:"notEvaluable,omitempty"`
	Namespaces      []string      `json:"namespaces"`
}

// Result is the impact of a change.
type Result struct {
	Summary     Summary   `json:"summary"`
	Blocking    bool      `json:"blocking"`
	Blockers    []Finding `json:"blockers,omitempty"`
	Info        []Finding `json:"info,omitempty"`
	NotVerified []Finding `json:"notVerified,omitempty"`
}

// maxListed caps how many dependents a line names; the rest become "+N more".
const maxListed = 5

// Evaluate judges set against the mirror graph. namespaces are the business's namespaces (-n): only
// changes in them are judged; nil or "all" judges every namespace the change touches.
func Evaluate(set *change.Set, g *report.Graph, namespaces []string) *Result {
	res := &Result{Summary: Summary{
		Format: set.Format, Changes: len(set.Changes), NonKubernetes: set.NonKubernetes,
		NoOp: set.NoOp, NotEvaluable: set.NotEvaluable,
	}}
	m := newMirror(g)

	// scope is nil when every namespace is judged: no -n, or "all".
	var scope map[string]bool
	if len(namespaces) > 0 {
		scope = map[string]bool{}
	}
	single := ""
	for _, ns := range namespaces {
		if ns == "all" {
			scope = nil
			break
		}
		scope[ns] = true
	}
	if len(scope) == 1 {
		for ns := range scope {
			single = ns
		}
	}

	// 1. Decide which changes are judged, and index the whole change (the bundle): a reference to an
	// object created in the same change is satisfied, one to an object it deletes is broken.
	createdNamespaces := map[string]bool{}
	applied, deleted := map[string]bool{}, map[string]bool{}
	var judged []change.Change
	for _, c := range set.Changes {
		if change.IsClusterScoped(c.Kind) {
			res.Summary.ClusterScoped++
			if c.Kind == "Namespace" && c.Action != change.Delete {
				createdNamespaces[c.Name] = true
			}
			continue
		}
		if c.Namespace == "" {
			if single == "" {
				res.Summary.NoNamespace++
				res.NotVerified = append(res.NotVerified, Finding{Object: objName(c.Kind, c.Name),
					Message: "has no namespace — pass the one namespace it deploys to with -n"})
				continue
			}
			c.Namespace = single
		}
		if c.Action == change.Delete {
			deleted[c.ID()] = true
		} else {
			applied[c.ID()] = true
		}
		if scope != nil && !scope[c.Namespace] {
			res.Summary.OtherNamespaces++
			continue
		}
		judged = append(judged, c)
	}
	res.Summary.Evaluated = len(judged)

	nsSeen := map[string]bool{}
	for _, c := range judged {
		if !nsSeen[c.Namespace] {
			nsSeen[c.Namespace] = true
			res.Summary.Namespaces = append(res.Summary.Namespaces, c.Namespace)
			if !m.namespaces[c.Namespace] && !createdNamespaces[c.Namespace] {
				res.NotVerified = append(res.NotVerified, Finding{Namespace: c.Namespace, Object: "namespace/" + c.Namespace,
					Message: "isn't in the mirror (empty, new, or outside the mirror's scope) — its references can't be checked"})
			}
		}
		res.judge(c, m, applied, deleted, m.namespaces[c.Namespace] || createdNamespaces[c.Namespace])
	}
	sort.Strings(res.Summary.Namespaces)
	res.Blocking = len(res.Blockers) > 0
	return res
}

// judge applies the rules to one change.
func (res *Result) judge(c change.Change, m *mirror, applied, deleted map[string]bool, nsKnown bool) {
	id := c.ID()
	node, exists := m.nodes[id]
	obj := objName(c.Kind, c.Name)

	if c.Action == change.Delete {
		users := m.usersExcept(id, deleted)
		deps := m.dependentsExcept(id, deleted)
		switch {
		case len(users) > 0 && guarded[c.Kind]:
			res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: c.Namespace,
				Message:    "is deleted, but " + m.describe(users, c.Namespace) + " still " + verb(len(users), "uses", "use") + " it" + m.beyond(users, deps),
				Dependents: m.labels(deps, c.Namespace)})
		case len(deps) > 0:
			res.Info = append(res.Info, Finding{Object: obj, Namespace: c.Namespace,
				Message:    "is deleted — " + m.describe(deps, c.Namespace) + " " + verb(len(deps), "depends", "depend") + " on it",
				Dependents: m.labels(deps, c.Namespace)})
		}
		return
	}

	if c.Action == change.Replace && guarded[c.Kind] {
		if users := m.usersExcept(id, deleted); len(users) > 0 {
			deps := m.dependentsExcept(id, deleted)
			res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: c.Namespace,
				Message:    "is replaced (deleted and recreated), and " + m.describe(users, c.Namespace) + " " + verb(len(users), "uses", "use") + " it" + m.beyond(users, deps),
				Dependents: m.labels(deps, c.Namespace)})
		}
	}

	// References the change adds. Those it already had are not judged again: a reference that was
	// already broken is the cluster's state, not this change's doing.
	prev := c.PrevRefs
	if !c.HasPrev {
		prev = m.refsOf(id)
	}
	var unverified []string
	for _, ref := range newRefs(c.Refs, prev) {
		rid := change.ID(ref.Kind, c.Namespace, ref.Name)
		target := objName(ref.Kind, ref.Name)
		switch {
		case deleted[rid]:
			res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: c.Namespace,
				Message: "uses " + target + ", which this change deletes"})
		case applied[rid]:
			// created or updated in the same change
		case ref.Kind == "PVC":
			res.checkPVC(obj, target, c.Namespace, m.nodes[rid], m.has(rid), nsKnown)
		case ref.Kind == "Service":
			if !m.has(rid) {
				if nsKnown {
					res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: c.Namespace,
						Message: "routes to " + target + ", which isn't in the cluster or this change"})
				} else {
					unverified = append(unverified, target)
				}
			}
		default: // ConfigMap, Secret: the report only lists the ones something already references
			unverified = append(unverified, target)
		}
	}
	if len(unverified) > 0 {
		res.NotVerified = append(res.NotVerified, Finding{Object: obj, Namespace: c.Namespace,
			Message: "uses " + strings.Join(unverified, ", ") + " — the mirror report can't confirm " +
				verb(len(unverified), "it exists", "they exist")})
	}
	if c.Kind == "Service" && !exists {
		res.NotVerified = append(res.NotVerified, Finding{Object: obj, Namespace: c.Namespace,
			Message: "is new — whether its selector matches any pods isn't checked (the report has no pod labels)"})
	}

	// Context: what's already wrong with the object, and what depends on it.
	if exists && troubled[node.Status] {
		res.Info = append(res.Info, Finding{Object: obj, Namespace: c.Namespace,
			Message: "is " + node.Status + " right now — this change may be the fix"})
	}
	if exists {
		if deps := m.dependentsExcept(id, deleted); len(deps) > 0 {
			res.Info = append(res.Info, Finding{Object: obj, Namespace: c.Namespace,
				Message:    "— " + m.describe(deps, c.Namespace) + " " + verb(len(deps), "depends", "depend") + " on it",
				Dependents: m.labels(deps, c.Namespace)})
		}
	}
}

func (res *Result) checkPVC(obj, target, ns string, pvc report.Component, exists, nsKnown bool) {
	switch {
	case exists && pvc.Status == "Lost":
		res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: ns, Message: "mounts " + target + ", which is Lost"})
	case exists && pvc.Status == "Pending":
		res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: ns,
			Message: "mounts " + target + ", which is Pending — pods that mount it won't start"})
	case exists:
		// Bound, or Pending (WaitForFirstConsumer), which binds when the pod is scheduled
	case nsKnown:
		res.Blockers = append(res.Blockers, Finding{Object: obj, Namespace: ns,
			Message: "mounts " + target + ", which isn't in the cluster or this change"})
	default:
		res.NotVerified = append(res.NotVerified, Finding{Object: obj, Namespace: ns,
			Message: "mounts " + target + " — its namespace isn't in the mirror, so it can't be checked"})
	}
}

// guarded are the kinds whose deletion blocks when something still uses them.
var guarded = map[string]bool{"PVC": true, "Service": true, "ConfigMap": true, "Secret": true}

// troubled are statuses worth mentioning about an object the change touches.
var troubled = map[string]bool{"Down": true, "Degraded": true, "Failed": true, "Lost": true, "Pending": true, "NotReady": true}

func newRefs(refs, prev []change.Ref) []change.Ref {
	had := map[change.Ref]bool{}
	for _, r := range prev {
		had[r] = true
	}
	var out []change.Ref
	for _, r := range refs {
		if !had[r] {
			out = append(out, r)
		}
	}
	return out
}

// --- the mirror graph ---------------------------------------------------------------------------

type mirror struct {
	nodes      map[string]report.Component
	dependents map[string][]string // id -> ids that depend on it (reverse edges)
	uses       map[string][]string // id -> ids it depends on (forward edges)
	namespaces map[string]bool
}

func newMirror(g *report.Graph) *mirror {
	m := &mirror{nodes: map[string]report.Component{}, dependents: map[string][]string{},
		uses: map[string][]string{}, namespaces: map[string]bool{}}
	if g == nil {
		return m
	}
	for _, n := range g.Nodes {
		m.nodes[n.ID] = n
		if n.Namespace != "" {
			m.namespaces[n.Namespace] = true
		}
	}
	for _, e := range g.Edges {
		m.dependents[e.To] = append(m.dependents[e.To], e.From)
		m.uses[e.From] = append(m.uses[e.From], e.To)
	}
	return m
}

func (m *mirror) has(id string) bool { _, ok := m.nodes[id]; return ok }

// refsOf returns the references an object has in the mirror today (its outgoing config, storage and
// routing edges), in the same form the change uses.
func (m *mirror) refsOf(id string) []change.Ref {
	var refs []change.Ref
	for _, to := range m.uses[id] {
		n, ok := m.nodes[to]
		if !ok {
			continue
		}
		switch n.Kind {
		case "ConfigMap", "Secret", "PVC", "Service":
			refs = append(refs, change.Ref{Kind: n.Kind, Name: n.Name})
		}
	}
	return refs
}

// usersExcept returns what depends on id directly, minus the components the change deletes too.
func (m *mirror) usersExcept(id string, skip map[string]bool) []string {
	var out []string
	for _, d := range m.dependents[id] {
		if !skip[d] {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// dependentsExcept returns everything that depends on id, directly or through other components — its
// blast radius. Components the change deletes too, and what depends only through them, are left out:
// what breaks there is the other deletion's doing.
func (m *mirror) dependentsExcept(id string, skip map[string]bool) []string {
	var out []string
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range m.dependents[cur] {
			if seen[d] || skip[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
			queue = append(queue, d)
		}
	}
	sort.Strings(out)
	return out
}

// beyond describes the blast radius past the direct users: " — and through them, 2 more depend on it".
func (m *mirror) beyond(users, deps []string) string {
	if n := len(deps) - len(users); n > 0 {
		return fmt.Sprintf(" — and through %s, %d more %s on it", verb(len(users), "it", "them"), n, verb(n, "depends", "depend"))
	}
	return ""
}

// labels names components as "kind/name", adding the namespace when it isn't ns.
func (m *mirror) labels(ids []string, ns string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		n := m.nodes[id]
		l := objName(n.Kind, n.Name)
		if n.Namespace != "" && n.Namespace != ns {
			l = n.Namespace + "/" + l
		}
		out = append(out, l)
	}
	return out
}

// describe lists up to maxListed dependents, then "+N more".
func (m *mirror) describe(ids []string, ns string) string {
	l := m.labels(ids, ns)
	if len(l) > maxListed {
		return strings.Join(l[:maxListed], ", ") + fmt.Sprintf(" and %d more", len(l)-maxListed)
	}
	return strings.Join(l, ", ")
}

func objName(kind, name string) string { return strings.ToLower(kind) + "/" + name }

func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
