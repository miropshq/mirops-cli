package cmd

import (
	"fmt"
	"strings"

	"github.com/miropshq/mirops-cli/internal/impact"
)

// maxIgnoredListed caps the ignored files printed; --output json has them all.
const maxIgnoredListed = 10

var formatNames = map[string]string{
	"yaml": "YAML", "json": "JSON", "terraform-plan": "Terraform plan", "mixed": "YAML and JSON",
}

// renderImpact prints the -f check: what was read, then what the change breaks (blocks), what it
// touches (info), and what the mirror report can't vouch for — never a manifest.
func renderImpact(res *impact.Result) {
	s := res.Summary
	fmt.Println()
	title := "IMPACT"
	if len(s.Namespaces) > 0 {
		title += " — " + strings.Join(s.Namespaces, ", ")
	}
	fmt.Println(boldc(title))

	format := formatNames[s.Format]
	if format == "" {
		format = s.Format
	}
	fmt.Printf("  read %s: %d Kubernetes change(s) → %d evaluated\n", format, s.Changes, s.Evaluated)
	var skipped []string
	add := func(n int, what string) {
		if n > 0 {
			skipped = append(skipped, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(s.NonKubernetes, "not Kubernetes")
	add(s.NoOp, "no-op")
	add(s.OtherNamespaces, "in other namespaces")
	add(s.ClusterScoped, "cluster-scoped")
	add(s.NoNamespace, "without a namespace")
	add(len(s.NotEvaluable), "not evaluable")
	if len(skipped) > 0 {
		fmt.Printf("  %s\n", dim("("+strings.Join(skipped, ", ")+")"))
	}
	if n := len(s.Ignored); n > 0 {
		fmt.Printf("  %s\n", dim(fmt.Sprintf("ignored %d file(s):", n)))
		for i, f := range s.Ignored {
			if i == maxIgnoredListed {
				fmt.Printf("    %s\n", dim(fmt.Sprintf("… and %d more (--output json lists them all)", n-i)))
				break
			}
			fmt.Printf("    %s %s\n", f.What, dim("— "+f.Reason))
		}
	}

	multiNS := len(s.Namespaces) > 1
	line := func(mark, obj, ns, msg string) {
		if multiNS && ns != "" {
			obj = ns + "/" + obj
		}
		fmt.Printf("  %s %s %s\n", mark, obj, msg)
	}

	fmt.Println()
	if len(res.Blockers) == 0 {
		fmt.Printf("  %s nothing this change touches is broken or still in use\n", green("✔"))
	}
	for _, f := range res.Blockers {
		line(red("✗ BLOCK"), f.Object, f.Namespace, f.Message)
	}
	for _, f := range res.Info {
		line(dim("ℹ"), f.Object, f.Namespace, dim(f.Message))
	}
	for _, f := range res.NotVerified {
		line(yellow("?"), f.Object, f.Namespace, dim(f.Message))
	}
	for _, n := range s.NotEvaluable {
		fmt.Printf("  %s %s %s\n", yellow("?"), n.What, dim("— "+n.Reason))
	}
}
