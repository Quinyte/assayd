// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package chart tests what `helm template` renders.
//
// A chart is where a platform's doctrine becomes deployable or quietly stops
// being true, so these are contract tests rather than smoke tests: the pod
// budget (rule 5), the stateful-dependency allowlist (rule 2), and the RBAC the
// operator actually needs. They need no cluster — rendering is enough — which
// makes them cheap enough to run on every change.
package chart

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

const chartPath = "../../charts/assayd"

// render runs `helm template` and returns the documents it produced.
func render(t *testing.T, extraArgs ...string) []map[string]any {
	t.Helper()
	var docs []map[string]any
	for _, d := range renderSourced(t, extraArgs...) {
		docs = append(docs, d.doc)
	}
	return docs
}

// sourcedDoc is one rendered document with the template Helm says produced
// it: the `# Source:` line, such as `assayd/templates/operator.yaml`, or
// `assayd/charts/nats/templates/statefulset.yaml` for a subchart's.
type sourcedDoc struct {
	source string
	doc    map[string]any
}

// renderSourced runs `helm template` and fails the test if it refuses.
func renderSourced(t *testing.T, extraArgs ...string) []sourcedDoc {
	t.Helper()
	out, err := helmTemplate(t, extraArgs...)
	if err != nil {
		t.Fatalf("helm template failed: %v", err)
	}
	return parseRendered(t, out)
}

// helmTemplate runs `helm template` with release name assayd and returns its
// standard output, which is the YAML and nothing else. Helm's standard error,
// where a refusal is written, is carried in the error.
func helmTemplate(t *testing.T, extraArgs ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatalf("helm is required to test the chart, and a skipped chart test is an " +
			"untested deployment: install helm")
	}
	args := append([]string{"template", "assayd", chartPath}, extraArgs...)
	var stderr strings.Builder
	cmd := exec.Command("helm", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, stderr.String())
	}
	return string(out), nil
}

// parseRendered splits a render into documents, keeping each one's `# Source:`.
// Helm writes its own Source line first, so the first one is taken: a
// template that writes a second cannot claim another template's path. A
// document with an `items` array, whatever its kind, is replaced by its
// items, each with the document's Source: the API machinery reads any such
// document as a list, and `helm install` creates every item in it, so a
// `kind: ConfigMap` carrying `items: [a PVC]` creates the PVC. A list nested
// in a list is refused by Helm's builder, so `helm install` fails on it;
// flattening it anyway is stricter than Helm and costs nothing.
func parseRendered(t *testing.T, out string) []sourcedDoc {
	t.Helper()
	return parseDocs(t, out, "")
}

// parseDocs splits YAML into documents. With source empty, each document's
// source is its first `# Source:` line; otherwise every document gets source.
func parseDocs(t *testing.T, out, fixedSource string) []sourcedDoc {
	t.Helper()
	var docs []sourcedDoc
	for _, chunk := range strings.Split(out, "\n---") {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(chunk), &doc); err != nil {
			t.Fatalf("parse rendered doc: %v\n%s", err, chunk)
		}
		if len(doc) == 0 {
			continue
		}
		source := fixedSource
		for _, line := range strings.Split(chunk, "\n") {
			if fixedSource != "" {
				break
			}
			if rest, ok := strings.CutPrefix(line, "# Source: "); ok {
				source = strings.TrimSpace(rest)
				break
			}
		}
		docs = append(docs, flattenLists(sourcedDoc{source: source, doc: doc})...)
	}
	return docs
}

func flattenLists(d sourcedDoc) []sourcedDoc {
	items, ok := d.doc["items"].([]any)
	if !ok {
		return []sourcedDoc{d}
	}
	var out []sourcedDoc
	for _, it := range items {
		if m := dig2(it); len(m) > 0 {
			out = append(out, flattenLists(sourcedDoc{source: d.source, doc: m})...)
		}
	}
	return out
}

// crdDocs reads every file under `crds/` in the chart at dir and in each of
// its subcharts, unpacked or packaged as a `.tgz`, at any depth. `helm install`
// creates every object in those files and `helm template` prints none of them
// unless asked, and then with no `# Source:` line, so they are read here and
// keyed by the path Helm would give them: `assayd/crds/<file>` for the
// chart's own, `assayd/charts/<sub>/crds/<file>` for a subchart's.
func crdDocs(t *testing.T, dir string) []sourcedDoc {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatalf("read chart %s: %v", dir, err)
	}
	return crdDocsOf(t, files, filepath.Base(dir))
}

// crdDocsOf reads crds/ from one chart's files, keyed by path relative to the
// chart, and recurses into charts/.
func crdDocsOf(t *testing.T, files map[string][]byte, prefix string) []sourcedDoc {
	t.Helper()
	var docs []sourcedDoc
	subcharts := map[string]map[string][]byte{}
	for rel, body := range files {
		switch {
		case strings.HasPrefix(rel, "crds/"):
			ext := strings.ToLower(filepath.Ext(rel))
			if ext == ".yaml" || ext == ".yml" || ext == ".json" {
				docs = append(docs, parseDocs(t, string(body), prefix+"/"+rel)...)
			}
		case strings.HasPrefix(rel, "charts/") && strings.HasSuffix(rel, ".tgz") && strings.Count(rel, "/") == 1:
			for name, sub := range untar(t, rel, body) {
				subcharts[name] = sub
			}
		case strings.HasPrefix(rel, "charts/") && strings.Count(rel, "/") >= 2:
			name, rest, _ := strings.Cut(strings.TrimPrefix(rel, "charts/"), "/")
			if subcharts[name] == nil {
				subcharts[name] = map[string][]byte{}
			}
			subcharts[name][rest] = body
		}
	}
	for name, sub := range subcharts {
		docs = append(docs, crdDocsOf(t, sub, prefix+"/charts/"+name)...)
	}
	return docs
}

// untar returns a packaged chart's files by chart name, then by path relative
// to that chart, as Helm packages it: every entry under one top directory.
func untar(t *testing.T, name string, body []byte) map[string]map[string][]byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("read packaged subchart %s: %v", name, err)
	}
	out := map[string]map[string][]byte{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read packaged subchart %s: %v", name, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		top, rest, ok := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		if !ok {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read packaged subchart %s: %v", name, err)
		}
		if out[top] == nil {
			out[top] = map[string][]byte{}
		}
		out[top][rest] = b
	}
	return out
}

func kindsOf(docs []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, d := range docs {
		if d["kind"] == kind {
			out = append(out, d)
		}
	}
	return out
}

// Rule 5: at most eight core pods, and the chart is the ledger. Counting per
// design 07 §5: sum of spec.replicas over Deployments and StatefulSets at the
// prod profile, DaemonSets as one logical each, Jobs and CronJobs excluded.
//
// This is the "written justification" gate made mechanical: a PR that adds a pod
// fails here unless it also changes the budget, which forces the change to be
// argued rather than absorbed.
func TestCorePodBudget(t *testing.T) {
	docs := render(t)

	total := 0
	breakdown := map[string]int{}
	for _, kind := range []string{"Deployment", "StatefulSet"} {
		for _, d := range kindsOf(docs, kind) {
			n := replicasOf(t, d)
			total += n
			breakdown[nameOf(d)] = n
		}
	}
	for _, d := range kindsOf(docs, "DaemonSet") {
		total++
		breakdown[nameOf(d)] = 1
	}

	const budget = 8
	if total > budget {
		t.Errorf("the core tier renders %d pods, and rule 5 budgets %d.\nBreakdown: %v\n"+
			"Adding a pod is allowed, but it must be argued: change the budget in the same "+
			"change that adds the pod.", total, budget, breakdown)
	}
	t.Logf("core pod count: %d/%d — %v", total, budget, breakdown)
}

// Rule 2, as ADR-0035 decided it: Postgres and NATS JetStream are the only
// stateful substrate, OpenObserve the only stateful sink, and SPIRE's CA
// signing keys the only stateful key material. The test is the one the ADR's
// Consequences specify, in five parts:
//
//  1. an entry matches one object exactly, on Helm's `# Source:` path, its kind
//     and its name (D3(a)), and what counts as stateful is decided deny by
//     default (D4(a)); both are tested on fixtures, below, with a positive
//     control, because the chart renders nothing stateful today;
//  2. the chart is rendered across renderMatrix and the objects are read as
//     one union; tier: plus is attempted and only its known refusal accepted;
//  3. every entry carries its class, its reason, its key and the ADR-0035
//     heading that records the human's decision (D7(a));
//  4. every entry and every hostPath exemption must match an object in the
//     union, so a renamed subchart cannot leave one standing unused;
//  5. entries arrive with the subcharts: SPIRE's key PVC as key material
//     (D2(a)), Postgres and NATS as substrate and openobserve-standalone as the
//     sink (D1(a)). None is rendered yet, so none is listed yet.
//
// What it reads: every document `helm template` prints on each row of
// renderMatrix, with the items of any document that carries an `items` array
// in place of the document; and every file under `crds/` in the chart and in
// each subchart, unpacked or `.tgz`, which `helm template` does not print.
//
// What it cannot see (rule 7): a storage-claiming kind nobody listed in
// storageClaimingKinds, since a render cannot know what another controller
// will create (D4); a pod template in a kind outside D4(a)'s seven, such as a
// ReplicationController; an object a template renders only when
// `.Capabilities` or `lookup` says the cluster has something, because
// `helm template` renders against no cluster and no --api-versions is passed;
// an object rendered only on an upgrade, through `.Release.IsUpgrade` or
// `.Release.Revision`, because `helm template` renders a first install;
// storage the operator creates at run time, which operator_storage_test.go
// covers (D5); an external service (D6, review only); and a values toggle
// nobody added to renderMatrix.
func TestStatefulDependencyAllowlist(t *testing.T) {
	headings := adrHeadings(t)
	var union []sourcedDoc
	for _, row := range renderMatrix {
		docs := renderSourced(t, row.args...)
		if len(docs) == 0 {
			t.Fatalf("the %s render produced no documents, so this test would pass by reading nothing", row.name)
		}
		union = append(union, docs...)

		// tier: plus refuses today. Once it renders, this fails: the change
		// that makes it render must add it to renderMatrix and settle
		// ADR-0035 Context's three open plus-tier rows (Phoenix, OpenFGA's
		// datastore, Argo's storage).
		plus := append(append([]string{}, row.args...), "--set", "tier=plus")
		if _, err := helmTemplate(t, plus...); err == nil {
			t.Errorf("tier: plus now renders on the %s row. Add it to renderMatrix, so its stateful "+
				"objects are read, and settle ADR-0035 Context's open plus-tier rows in the same change.", row.name)
		} else if !strings.Contains(err.Error(), plusRefusal) {
			t.Errorf("tier: plus failed on the %s row, and not with its known refusal (%q), so what "+
				"it would render is unread:\n%v", row.name, plusRefusal, err)
		}
	}

	// crds/ is values-independent, so it is read once. The CRDs there today are
	// CustomResourceDefinitions, which hold no storage.
	crds := crdDocs(t, chartPath)
	if len(crds) == 0 {
		t.Fatal("read no document under the chart's crds/, which ships the Agent CRD: the reader is broken")
	}
	union = append(union, crds...)

	problems, stateful := checkStatefulAllowlist(union, statefulAllowlist, hostPathExempt, headings)
	for _, p := range problems {
		t.Error(p)
	}
	// Printed only under -v, and `make chart` runs without it: the fixture
	// test, not this line, is what shows the check is not vacuous.
	t.Logf("read %d documents from %d renders and crds/; %d distinct stateful objects", len(union), len(renderMatrix), stateful)
}

// renderMatrix is ADR-0035's render matrix: the local profile or not, crossed
// with the gateway on or off. gateway.enabled=true needs gateway.servingUrl,
// or templates/operator.yaml fails the render.
var renderMatrix = []struct {
	name string
	args []string
}{
	{"prod", nil},
	{"local", []string{"-f", filepath.Join(chartPath, "values-local.yaml")}},
	{"prod+gateway", []string{"--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080"}},
	{"local+gateway", []string{"-f", filepath.Join(chartPath, "values-local.yaml"),
		"--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080"}},
}

// plusRefusal is the start of the message templates/_helpers.tpl fails with.
const plusRefusal = "tier: plus is not implemented"

// statefulClass is an entry's class, defined by authority (ADR-0035 D1(a),
// D2(a)): a substrate is read to take a decision or answer an audit question,
// a sink never is, and key material signs and is never read to decide.
type statefulClass string

const (
	substrate   statefulClass = "substrate"
	sink        statefulClass = "sink"
	keyMaterial statefulClass = "key material"
)

// objectKey names one rendered object exactly (ADR-0035 D3(a)). The source is
// Helm's own `# Source:` path, so a subchart's carries its `assayd/charts/…`
// prefix, which is what tells the platform's `StatefulSet assayd-nats` from
// the one OpenObserve's HA chart would render under the same name.
type objectKey struct{ source, kind, name string }

func (k objectKey) String() string { return fmt.Sprintf("%s %q (%s)", k.kind, k.name, k.source) }

// statefulEntry admits one stateful object. decision is the heading of
// ADR-0035 that records the human's decision: "D1" to "D7", or "Amendment N".
// The test checks the heading exists; that a human decided is review's job.
type statefulEntry struct {
	key      objectKey
	class    statefulClass
	reason   string
	decision string
}

// statefulAllowlist is empty because the chart renders no subchart yet. A
// change that introduces a stateful object adds it here with its class and
// reason, and adds a numbered `## Amendment N` to ADR-0035 recording the
// human's decision (D7(a)).
var statefulAllowlist = []statefulEntry{}

// hostPathExemption exempts one hostPath volume exactly: the object that
// mounts it, the volume's name and its host path (ADR-0035 D4(a)). The first
// are SPIRE's socket mounts, which arrive with the SPIRE subchart; they carry
// sockets, not records.
type hostPathExemption struct {
	key      objectKey
	volume   string
	path     string
	reason   string
	decision string
}

var hostPathExempt = []hostPathExemption{}

// podBearingKinds are the kinds whose pod template D4(a) reads.
var podBearingKinds = map[string]bool{
	"Pod": true, "Deployment": true, "ReplicaSet": true, "StatefulSet": true,
	"DaemonSet": true, "Job": true, "CronJob": true,
}

// harmlessVolumes are the only volume sources D4(a) does not count. Every
// other source counts, including any it has never heard of.
var harmlessVolumes = map[string]bool{
	"configMap": true, "secret": true, "projected": true, "downwardAPI": true,
	"emptyDir": true, "image": true,
}

// spiffeCSIDriver is the one inline csi driver D4(a) does not count.
const spiffeCSIDriver = "csi.spiffe.io"

// storageClaimingKinds are "group/Kind" pairs whose controller creates
// storage from the object (ADR-0035 D4(a)).
var storageClaimingKinds = map[string]string{
	"postgresql.cnpg.io/Cluster":                 "CNPG creates Pods and PVCs from a Cluster",
	"agents.x-k8s.io/Sandbox":                    "agent-sandbox creates PVCs from its volumeClaimTemplates",
	"extensions.agents.x-k8s.io/SandboxTemplate": "a Sandbox made from it claims storage",
}

// statefulness returns why a rendered object counts as stateful under
// ADR-0035 D4(a), one reason per cause, or nothing. A hostPath volume that an
// exemption matches is not a cause, and marks that exemption used.
func statefulness(d sourcedDoc, exempt []hostPathExemption, used map[int]bool) []string {
	kind := toStr(d.doc["kind"])
	group, _, found := strings.Cut(toStr(d.doc["apiVersion"]), "/")
	if !found {
		group = "" // the core group: apiVersion "v1"
	}
	key := objectKey{d.source, kind, nameOf(d.doc)}

	var reasons []string
	switch kind {
	case "PersistentVolumeClaim", "PersistentVolume":
		reasons = append(reasons, "it is a "+kind)
	}
	if why, ok := storageClaimingKinds[group+"/"+kind]; ok {
		reasons = append(reasons, "it is a storage-claiming kind: "+why)
	}
	if kind == "StatefulSet" && len(toList(dig(d.doc, "spec")["volumeClaimTemplates"])) > 0 {
		reasons = append(reasons, "it has volumeClaimTemplates")
	}
	if !podBearingKinds[kind] {
		return reasons
	}

	var spec map[string]any
	switch kind {
	case "Pod":
		spec = dig(d.doc, "spec")
	case "CronJob":
		spec = dig(d.doc, "spec", "jobTemplate", "spec", "template", "spec")
	default:
		spec = dig(d.doc, "spec", "template", "spec")
	}
	for _, v := range toList(spec["volumes"]) {
		vol := dig2(v)
		name := toStr(vol["name"])
		var sources []string
		for k := range vol {
			if k != "name" {
				sources = append(sources, k)
			}
		}
		if len(sources) == 0 {
			continue // the API server defaults a volume with no source to emptyDir
		}
		if len(sources) > 1 {
			reasons = append(reasons, fmt.Sprintf("volume %q has %d sources, and only one known "+
				"harmless source is not counted", name, len(sources)))
			continue
		}
		src := sources[0]
		switch {
		case harmlessVolumes[src]:
		case src == "csi" && toStr(dig(vol, "csi")["driver"]) == spiffeCSIDriver:
		case src == "hostPath" && exemptHostPath(key, name, toStr(dig(vol, "hostPath")["path"]), exempt, used):
		default:
			reasons = append(reasons, fmt.Sprintf("volume %q has source %s", name, src))
		}
	}
	return reasons
}

func exemptHostPath(key objectKey, volume, path string, exempt []hostPathExemption, used map[int]bool) bool {
	for i, e := range exempt {
		if e.key == key && e.volume == volume && e.path == path {
			used[i] = true
			return true
		}
	}
	return false
}

// admitted reports whether an entry names this object exactly.
func admitted(key objectKey, allow []statefulEntry) bool {
	for _, e := range allow {
		if e.key == key {
			return true
		}
	}
	return false
}

// checkStatefulAllowlist reads the union of rendered documents and returns
// every problem, and the number of distinct stateful objects it read. An
// object rendered by more than one row of the matrix is one object.
func checkStatefulAllowlist(docs []sourcedDoc, allow []statefulEntry, exempt []hostPathExemption,
	headings map[string]bool) (problems []string, stateful int) {
	for _, e := range allow {
		if e.key.source == "" || e.key.kind == "" || e.key.name == "" {
			problems = append(problems, fmt.Sprintf("allowlist entry %s does not name its object by "+
				"source, kind and name (ADR-0035 D3(a))", e.key))
		}
		if e.class != substrate && e.class != sink && e.class != keyMaterial {
			problems = append(problems, fmt.Sprintf("allowlist entry %s has class %q; ADR-0035 D1(a) "+
				"and D2(a) define substrate, sink and key material", e.key, e.class))
		}
		if strings.TrimSpace(e.reason) == "" {
			problems = append(problems, fmt.Sprintf("allowlist entry %s gives no reason", e.key))
		}
		if !headings[e.decision] {
			problems = append(problems, fmt.Sprintf("allowlist entry %s cites %q, which is not a heading "+
				"of ADR-0035: an entry needs a `## Amendment N` recording the human's decision (D7(a))",
				e.key, e.decision))
		}
	}
	for _, e := range exempt {
		if strings.TrimSpace(e.reason) == "" || !headings[e.decision] {
			problems = append(problems, fmt.Sprintf("hostPath exemption %s volume %q needs a reason and "+
				"an ADR-0035 heading; it cites %q", e.key, e.volume, e.decision))
		}
	}

	used := map[int]bool{}
	seen := map[objectKey]bool{}
	for _, d := range docs {
		key := objectKey{d.source, toStr(d.doc["kind"]), nameOf(d.doc)}
		if key.source == "" {
			problems = append(problems, fmt.Sprintf("%s has no `# Source:` line, so no entry can name it", key))
		}
		reasons := statefulness(d, exempt, used)
		if len(reasons) == 0 || seen[key] {
			continue
		}
		seen[key] = true
		stateful++
		if !admitted(key, allow) {
			problems = append(problems, fmt.Sprintf("%s is stateful (%s) and not on the allowlist. "+
				"NFR-2 admits only Postgres and NATS JetStream as substrate, OpenObserve as the sink and "+
				"SPIRE's CA keys as key material: classify it, add a `## Amendment N` to ADR-0035 recording "+
				"the human's decision, and add the entry citing it.", key, strings.Join(reasons, "; ")))
		}
	}
	for _, e := range allow {
		if !seen[e.key] {
			problems = append(problems, fmt.Sprintf("allowlist entry %s matches no stateful object in any "+
				"render; remove it, or correct its key", e.key))
		}
	}
	for i, e := range exempt {
		if !used[i] {
			problems = append(problems, fmt.Sprintf("hostPath exemption %s volume %q path %q matches no "+
				"rendered volume; remove it, or correct it", e.key, e.volume, e.path))
		}
	}
	return problems, stateful
}

const adr0035 = "../../docs/decisions/0035-nfr2-stateful-dependency-allowlist.md"

// adrHeadingRE finds the headings an entry may cite: `## D1 — …` to
// `## D7 — …`, and any `## Amendment N` added later.
var adrHeadingRE = regexp.MustCompile(`(?m)^## (D[0-9]+|Amendment [0-9]+)\b`)

func adrHeadings(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(adr0035)
	if err != nil {
		t.Fatalf("read ADR-0035, whose headings the allowlist cites: %v", err)
	}
	out := map[string]bool{}
	for _, m := range adrHeadingRE.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = true
	}
	for _, want := range []string{"D1", "D2", "D3", "D4", "D5", "D6", "D7"} {
		if !out[want] {
			t.Fatalf("ADR-0035 has no `## %s` heading, so the heading check reads the wrong file or "+
				"the wrong pattern: %v", want, out)
		}
	}
	return out
}

// The fixtures for TestStatefulDependencyAllowlist. The chart renders nothing
// stateful today, so a matcher and a classifier tested only against the real
// render could refuse nothing, or everything, and pass. Each row is a planted
// document with the verdict ADR-0035 requires; the first is the positive
// control, an exact entry that must be admitted.
func TestStatefulAllowlistOnFixtures(t *testing.T) {
	const natsSrc = "assayd/charts/nats/templates/statefulset.yaml"
	const spireAgentSrc = "assayd/charts/spire/charts/spire-agent/templates/daemonset.yaml"
	const podTemplate = "  template:\n    spec:\n      containers: [{name: c, image: i}]\n      volumes:\n"
	vct := "  volumeClaimTemplates:\n  - metadata: {name: data}\n    spec: {accessModes: [ReadWriteOnce]}\n"

	rows := []struct {
		why          string
		source, body string
		stateful     bool // counts under D4(a)
		refused      bool // and is not admitted
	}{
		{"positive control: the exact entry is admitted", natsSrc,
			"apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: assayd-nats}\nspec:\n" + vct, true, false},
		{"a name that merely contains an entry's is refused (D3(a), not a substring)", natsSrc,
			"apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: assayd-nats-sidecar-cache}\nspec:\n" + vct, true, true},
		{"the same kind and name from another subchart is refused (the Source path is the key)",
			"assayd/charts/openobserve/charts/nats/templates/statefulset.yaml",
			"apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: assayd-nats}\nspec:\n" + vct, true, true},
		{"an nfs volume counts", "assayd/templates/nfs.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: nfs}\nspec:\n" + podTemplate +
				"      - {name: d, nfs: {server: s, path: /x}}\n", true, true},
		{"a PersistentVolume counts", "assayd/templates/pv.yaml",
			"apiVersion: v1\nkind: PersistentVolume\nmetadata: {name: pv}\nspec: {capacity: {storage: 1Gi}}\n", true, true},
		{"a PersistentVolumeClaim counts", "assayd/templates/pvc.yaml",
			"apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: pvc}\nspec: {}\n", true, true},
		{"a CNPG Cluster counts, though it renders no StatefulSet", "assayd/charts/cluster/templates/cluster.yaml",
			"apiVersion: postgresql.cnpg.io/v1\nkind: Cluster\nmetadata: {name: assayd-postgres}\nspec: {instances: 1}\n", true, true},
		{"an agent-sandbox Sandbox counts", "assayd/templates/sandbox.yaml",
			"apiVersion: agents.x-k8s.io/v1alpha1\nkind: Sandbox\nmetadata: {name: sb}\nspec: {}\n", true, true},
		{"an agent-sandbox SandboxTemplate counts", "assayd/templates/sandboxtemplate.yaml",
			"apiVersion: extensions.agents.x-k8s.io/v1alpha1\nkind: SandboxTemplate\nmetadata: {name: sbt}\nspec: {}\n", true, true},
		{"a CronJob's PVC volume counts", "assayd/templates/cronjob.yaml",
			"apiVersion: batch/v1\nkind: CronJob\nmetadata: {name: cj}\nspec:\n  schedule: '* * * * *'\n  jobTemplate:\n    spec:\n" +
				"      template:\n        spec:\n          containers: [{name: c, image: i}]\n          volumes:\n" +
				"          - {name: d, persistentVolumeClaim: {claimName: x}}\n", true, true},
		{"a Pod's ephemeral volume counts", "assayd/templates/pod.yaml",
			"apiVersion: v1\nkind: Pod\nmetadata: {name: p}\nspec:\n  containers: [{name: c, image: i}]\n  volumes:\n" +
				"  - {name: d, ephemeral: {volumeClaimTemplate: {spec: {}}}}\n", true, true},
		{"an inline csi volume with another driver counts", "assayd/templates/csi.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: csi}\nspec:\n" + podTemplate +
				"      - {name: d, csi: {driver: ebs.csi.aws.com}}\n", true, true},
		{"a volume source nobody listed counts (deny by default)", "assayd/templates/unknown.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: unknown}\nspec:\n" + podTemplate +
				"      - {name: d, futureDisk: {size: 1Gi}}\n", true, true},
		{"a hostPath nobody exempted counts", "assayd/templates/host.yaml",
			"apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: host}\nspec:\n" + podTemplate +
				"      - {name: spire-agent-socket-dir, hostPath: {path: /run/spire/agent-sockets}}\n", true, true},
		{"an exempt object's hostPath at another path counts", spireAgentSrc,
			"apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: assayd-agent-moved}\nspec:\n" + podTemplate +
				"      - {name: spire-agent-socket-dir, hostPath: {path: /var/lib/spire}}\n", true, true},
		{"an entry admits its kind only: a PVC with the entry's source and name is refused", natsSrc,
			"apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: assayd-nats}\nspec: {}\n", true, true},
		{"an exemption admits its Source only", "assayd/charts/other/templates/daemonset.yaml",
			"apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: assayd-agent}\nspec:\n" + podTemplate +
				"      - {name: spire-agent-socket-dir, hostPath: {path: /run/spire/agent-sockets}}\n", true, true},
		{"an exemption admits its kind only", spireAgentSrc,
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: assayd-agent}\nspec:\n" + podTemplate +
				"      - {name: spire-agent-socket-dir, hostPath: {path: /run/spire/agent-sockets}}\n", true, true},
		{"an exemption admits its name only", spireAgentSrc,
			"apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: assayd-agent-2}\nspec:\n" + podTemplate +
				"      - {name: spire-agent-socket-dir, hostPath: {path: /run/spire/agent-sockets}}\n", true, true},
		{"an exemption admits its volume name only", spireAgentSrc,
			"apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: assayd-agent-vol}\nspec:\n" + podTemplate +
				"      - {name: other-dir, hostPath: {path: /run/spire/agent-sockets}}\n", true, true},
		{"a Job's hostPath volume counts", "assayd/templates/job.yaml",
			"apiVersion: batch/v1\nkind: Job\nmetadata: {name: job}\nspec:\n" + podTemplate +
				"      - {name: d, hostPath: {path: /var/data}}\n", true, true},
		{"a ReplicaSet's nfs volume counts", "assayd/templates/rs.yaml",
			"apiVersion: apps/v1\nkind: ReplicaSet\nmetadata: {name: rs}\nspec:\n" + podTemplate +
				"      - {name: d, nfs: {server: s, path: /x}}\n", true, true},
		{"a StatefulSet with no claim templates is counted by its pod volumes", "assayd/templates/sts-host.yaml",
			"apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: sts-host}\nspec:\n" + podTemplate +
				"      - {name: d, hostPath: {path: /var/data}}\n", true, true},
		{"a volume with no source passes, since the API server defaults it to emptyDir", "assayd/templates/nosource.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: nosource}\nspec:\n" + podTemplate +
				"      - {name: d}\n", false, false},
		{"a volume with two sources counts, though one is harmless", "assayd/templates/twosources.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: twosources}\nspec:\n" + podTemplate +
				"      - {name: d, emptyDir: {}, hostPath: {path: /var/data}}\n", true, true},
		{"a PVC inside a List counts, since helm install creates every item", "assayd/templates/list.yaml",
			"apiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: PersistentVolumeClaim\n  metadata: {name: listed}\n  spec: {}\n", true, true},
		{"a PVC inside a typed PersistentVolumeClaimList counts", "assayd/templates/pvclist.yaml",
			"apiVersion: v1\nkind: PersistentVolumeClaimList\nitems:\n- apiVersion: v1\n  kind: PersistentVolumeClaim\n" +
				"  metadata: {name: typed-listed}\n  spec: {}\n", true, true},
		{"a PVC inside a List inside a List counts", "assayd/templates/nested.yaml",
			"apiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: List\n  items:\n  - apiVersion: v1\n" +
				"    kind: PersistentVolumeClaim\n    metadata: {name: nested}\n    spec: {}\n", true, true},
		{"a PVC in the items of a document of any kind counts", "assayd/templates/cm-items.yaml",
			"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: carrier}\nitems:\n- apiVersion: v1\n  kind: PersistentVolumeClaim\n" +
				"  metadata: {name: carried}\n  spec: {}\n", true, true},
		{"a template's own Source line cannot claim an entry's path", "assayd/templates/claims-nats.yaml",
			"# Source: " + natsSrc + "\napiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: assayd-nats}\nspec:\n" + vct, true, true},
		{"every harmless volume source, and csi.spiffe.io, passes", "assayd/templates/harmless.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: harmless}\nspec:\n" + podTemplate +
				"      - {name: a, emptyDir: {}}\n      - {name: b, configMap: {name: x}}\n      - {name: c, secret: {secretName: x}}\n" +
				"      - {name: d, projected: {sources: []}}\n      - {name: e, downwardAPI: {items: []}}\n" +
				"      - {name: f, image: {reference: r}}\n      - {name: g, csi: {driver: csi.spiffe.io, readOnly: true}}\n", false, false},
		{"a StatefulSet is counted by its storage, not its kind", "assayd/templates/sts.yaml",
			"apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: scratch}\nspec:\n" + podTemplate +
				"      - {name: a, emptyDir: {}}\n", false, false},
		{"SPIRE's exempt socket mount passes", spireAgentSrc,
			"apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: assayd-agent}\nspec:\n" + podTemplate +
				"      - {name: spire-agent-socket-dir, hostPath: {path: /run/spire/agent-sockets}}\n", false, false},
		{"an object with no storage passes", "assayd/templates/cm.yaml",
			"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm}\ndata: {a: b}\n", false, false},
	}

	var rendered strings.Builder
	for _, r := range rows {
		rendered.WriteString("---\n# Source: " + r.source + "\n" + r.body)
	}
	docs := parseRendered(t, rendered.String())
	if len(docs) != len(rows) {
		t.Fatalf("parsed %d fixture documents from %d rows", len(docs), len(rows))
	}

	natsKey := objectKey{natsSrc, "StatefulSet", "assayd-nats"}
	allow := []statefulEntry{
		{natsKey, substrate, "the platform's NATS JetStream", "D1"},
		{objectKey{"assayd/charts/gone/templates/statefulset.yaml", "StatefulSet", "assayd-gone"}, sink, "renamed away", "D1"},
		{objectKey{"assayd/charts/x/templates/a.yaml", "StatefulSet", "undecided"}, substrate, "no amendment", "Amendment 99"},
		{objectKey{"assayd/charts/x/templates/b.yaml", "StatefulSet", "cache"}, "cache", "", "D1"},
		{objectKey{"", "StatefulSet", "sourceless"}, substrate, "no source", "D1"},
	}
	exempt := []hostPathExemption{
		{objectKey{spireAgentSrc, "DaemonSet", "assayd-agent"}, "spire-agent-socket-dir", "/run/spire/agent-sockets",
			"the Workload API socket, not a record", "D4"},
		{objectKey{spireAgentSrc, "DaemonSet", "assayd-agent-moved"}, "spire-agent-socket-dir", "/run/spire/agent-sockets",
			"exempt at its socket path only", "D4"},
		{objectKey{spireAgentSrc, "DaemonSet", "assayd-agent-vol"}, "spire-agent-socket-dir", "/run/spire/agent-sockets",
			"exempt for its volume name only", "D4"},
		{objectKey{"assayd/charts/x/templates/c.yaml", "DaemonSet", "no-reason"}, "v", "/p", "", "D4"},
		{objectKey{"assayd/charts/x/templates/d.yaml", "DaemonSet", "undecided"}, "v", "/p", "a reason", "Amendment 99"},
	}
	problems, stateful := checkStatefulAllowlist(docs, allow, exempt, adrHeadings(t))

	wantStateful := 0
	for i, r := range rows {
		key := objectKey{r.source, toStr(docs[i].doc["kind"]), nameOf(docs[i].doc)}
		if got := len(statefulness(docs[i], exempt, map[int]bool{})) > 0; got != r.stateful {
			t.Errorf("%s: %s counted stateful=%v, want %v", r.why, key, got, r.stateful)
		}
		if r.stateful {
			wantStateful++
		}
		refused := false
		for _, p := range problems {
			refused = refused || strings.HasPrefix(p, key.String()+" is stateful")
		}
		if refused != r.refused {
			t.Errorf("%s: %s refused=%v, want %v\nproblems:\n%s", r.why, key, refused, r.refused,
				strings.Join(problems, "\n"))
		}
	}
	if stateful != wantStateful {
		t.Errorf("counted %d stateful objects, want %d", stateful, wantStateful)
	}

	// Part 3 and part 4: each malformed or unused entry is reported, and the
	// positive control's entry, which cites D1 and matched, is not.
	for _, want := range []string{
		`StatefulSet "assayd-gone" (assayd/charts/gone/templates/statefulset.yaml) matches no stateful object`,
		`cites "Amendment 99", which is not a heading of ADR-0035`,
		`has class "cache"`,
		`allowlist entry StatefulSet "sourceless" () does not name its object by source, kind and name`,
		`StatefulSet "cache" (assayd/charts/x/templates/b.yaml) gives no reason`,
		`hostPath exemption DaemonSet "assayd-agent-moved" (` + spireAgentSrc + `) volume "spire-agent-socket-dir" path "/run/spire/agent-sockets" matches no rendered volume`,
		`hostPath exemption DaemonSet "assayd-agent-vol" (` + spireAgentSrc + `) volume "spire-agent-socket-dir" path "/run/spire/agent-sockets" matches no rendered volume`,
		`hostPath exemption DaemonSet "no-reason" (assayd/charts/x/templates/c.yaml) volume "v" needs a reason and an ADR-0035 heading`,
		`hostPath exemption DaemonSet "undecided" (assayd/charts/x/templates/d.yaml) volume "v" needs a reason and an ADR-0035 heading`,
	} {
		found := false
		for _, p := range problems {
			found = found || strings.Contains(p, want)
		}
		if !found {
			t.Errorf("no problem reports %q:\n%s", want, strings.Join(problems, "\n"))
		}
	}
	for _, p := range problems {
		matchedExemption := strings.Contains(p, `DaemonSet "assayd-agent" (`) && strings.Contains(p, "matches no rendered volume")
		if strings.Contains(p, natsKey.String()) || matchedExemption {
			t.Errorf("a well-formed entry or exemption that matched was reported: %s", p)
		}
	}

	// A document without a `# Source:` line cannot be named by any entry.
	if p, _ := checkStatefulAllowlist(parseRendered(t, "---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: x}\n"),
		nil, nil, adrHeadings(t)); len(p) != 1 || !strings.Contains(p[0], "no `# Source:` line") {
		t.Errorf("a document with no Source line was not reported: %v", p)
	}
}

// Only helm's standard output is YAML. A warning on standard error mixed
// into it would be parsed as a document, or fail the parse; a refusal on
// standard error must still reach the caller, in the error.
func TestHelmTemplateParsesStandardOutputOnly(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo 'WARNING: this is not YAML: {[' >&2\n" +
		"printf -- '---\\n# Source: assayd/templates/a.yaml\\napiVersion: v1\\nkind: ConfigMap\\nmetadata: {name: a}\\n'\n" +
		"case \"$*\" in *refuse*) echo 'Error: refused on stderr' >&2; exit 1;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	docs := renderSourced(t)
	if len(docs) != 1 || docs[0].source != "assayd/templates/a.yaml" {
		t.Errorf("want the one document on standard output, got %v", docs)
	}
	if _, err := helmTemplate(t, "refuse"); err == nil || !strings.Contains(err.Error(), "refused on stderr") {
		t.Errorf("a refusal written to standard error did not reach the caller: %v", err)
	}
}

// crds/ is read in the chart and every subchart, unpacked or packaged, at any
// depth, under the path Helm gives it; what is there is classified like any
// rendered object, and a file that is not a manifest is skipped as Helm skips it.
func TestCRDsDirectoriesAreRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "assayd")
	pvc := func(name string) string {
		return "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: " + name + "}\nspec: {}\n"
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Chart.yaml", "name: assayd\n")
	write("templates/pvc.yaml", pvc("templated")) // helm template's business, not this reader's
	write("crds/zz-probe.yaml", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: x}\n---\n"+pvc("own"))
	write("crds/nested/pv.json", `{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": {"name": "nested"}}`)
	write("crds/README.md", pvc("not-a-manifest"))
	write("charts/sub/crds/sandbox.yaml", "apiVersion: agents.x-k8s.io/v1alpha1\nkind: Sandbox\nmetadata: {name: sub}\n")

	var tgz bytes.Buffer
	zw := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(zw)
	for name, body := range map[string]string{
		"pkg/Chart.yaml":                 "name: pkg\n",
		"pkg/crds/pvc.yaml":              pvc("packaged"),
		"pkg/charts/inner/crds/pvc.yaml": pvc("inner"),
		"pkg/templates/not-a-crd.yaml":   pvc("pkg-templated"),
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	write("charts/pkg-1.0.0.tgz", tgz.String())

	docs := crdDocs(t, dir)
	got := map[string]bool{}
	for _, d := range docs {
		got[objectKey{d.source, toStr(d.doc["kind"]), nameOf(d.doc)}.String()] = true
	}
	want := []objectKey{
		{"assayd/crds/zz-probe.yaml", "CustomResourceDefinition", "x"},
		{"assayd/crds/zz-probe.yaml", "PersistentVolumeClaim", "own"},
		{"assayd/crds/nested/pv.json", "PersistentVolume", "nested"},
		{"assayd/charts/sub/crds/sandbox.yaml", "Sandbox", "sub"},
		{"assayd/charts/pkg/crds/pvc.yaml", "PersistentVolumeClaim", "packaged"},
		{"assayd/charts/pkg/charts/inner/crds/pvc.yaml", "PersistentVolumeClaim", "inner"},
	}
	for _, k := range want {
		if !got[k.String()] {
			t.Errorf("crds/ reader did not return %s; got %v", k, got)
		}
	}
	if len(docs) != len(want) {
		t.Errorf("crds/ reader returned %d documents, want %d (a template or a non-manifest was read): %v",
			len(docs), len(want), got)
	}

	problems, stateful := checkStatefulAllowlist(docs, nil, nil, adrHeadings(t))
	if stateful != 5 || len(problems) != 5 {
		t.Errorf("want the five stateful objects under crds/ refused, got %d stateful and problems:\n%s",
			stateful, strings.Join(problems, "\n"))
	}
}

// The operator's ClusterRole must grant what the code's kubebuilder markers ask
// for. B2 was exactly this drifting: leader election defaulted on while the
// role granted no leases, and the operator retried a forbidden lease forever
// while reporting Ready.
func TestOperatorRBACCoversWhatTheOperatorNeeds(t *testing.T) {
	docs := render(t)
	roles := kindsOf(docs, "ClusterRole")
	if len(roles) == 0 {
		t.Fatal("the chart renders no ClusterRole, so the operator cannot read Agents")
	}

	granted := map[string]bool{}
	for _, role := range roles {
		rules, _ := role["rules"].([]any)
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			for _, g := range toStrings(rule["apiGroups"]) {
				for _, res := range toStrings(rule["resources"]) {
					granted[g+"/"+res] = true
				}
			}
		}
	}

	for _, need := range []struct{ perm, why string }{
		{"assayd.dev/agents", "the object this operator exists to reconcile"},
		{"assayd.dev/agents/status", "conditions and phase are how it reports anything"},
		{"apps/deployments", "the workload it materializes"},
		{"coordination.k8s.io/leases", "leader election defaults on, and a forbidden lease " +
			"is retried forever rather than reported — the operator would run, report Ready, " +
			"and reconcile nothing"},
		{"apiextensions.k8s.io/customresourcedefinitions", "whether the EvalSuite CRD is " +
			"installed decides whether rollouts are eval-gated (ADR-0006)"},
		{"/events", "the durable record design 02 §3.3 promises for a superseded candidate"},
	} {
		if !granted[need.perm] {
			t.Errorf("the ClusterRole does not grant %s — %s", need.perm, need.why)
		}
	}
}

// The operator's own pod is held to the standard it holds agents to. A control
// plane that hardens its workloads and not itself is making an argument it does
// not believe.
func TestOperatorPodIsHardened(t *testing.T) {
	docs := render(t)
	deploys := kindsOf(docs, "Deployment")
	if len(deploys) == 0 {
		t.Fatal("the chart renders no operator Deployment")
	}

	for _, d := range deploys {
		spec := dig(d, "spec", "template", "spec")
		if spec == nil {
			t.Fatalf("%s has no pod spec", nameOf(d))
		}
		if v, _ := dig(spec, "securityContext")["runAsNonRoot"].(bool); !v {
			t.Errorf("%s may run as root", nameOf(d))
		}
		if v, ok := spec["automountServiceAccountToken"].(bool); ok && v {
			t.Errorf("%s automounts a service-account token it does not need", nameOf(d))
		}
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			container, _ := c.(map[string]any)
			sc := dig(container, "securityContext")
			if v, _ := sc["readOnlyRootFilesystem"].(bool); !v {
				t.Errorf("%s/%v has a writable root filesystem", nameOf(d), container["name"])
			}
			if v, ok := sc["allowPrivilegeEscalation"].(bool); !ok || v {
				t.Errorf("%s/%v allows privilege escalation", nameOf(d), container["name"])
			}
			if container["image"] == nil || strings.HasSuffix(toStr(container["image"]), ":latest") {
				t.Errorf("%s/%v uses a floating image tag: a rollout would not be reproducible",
					nameOf(d), container["name"])
			}
		}
	}
}

// The CRD ships in crds/, and it must be the one the operator was built against.
// A chart carrying a stale CRD installs a cluster the operator cannot serve.
func TestChartShipsTheGeneratedCRD(t *testing.T) {
	shipped, err := os.ReadFile(filepath.Join(chartPath, "crds", "assayd.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("the chart ships no Agent CRD: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "assayd.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("read generated CRD: %v", err)
	}
	if string(shipped) != string(generated) {
		t.Error("the CRD in charts/assayd/crds differs from config/crd. The chart would " +
			"install a schema the operator was not built against; re-copy it in the same " +
			"change that regenerates it.")
	}
}

// The local profile is the honest mode: single replicas, and it must still
// render. NFR-3 says this runs on a laptop, and a profile nobody renders is a
// profile that does not work.
func TestLocalProfileRenders(t *testing.T) {
	docs := render(t, "-f", filepath.Join(chartPath, "values-local.yaml"))
	for _, d := range kindsOf(docs, "Deployment") {
		if n := replicasOf(t, d); n != 1 {
			t.Errorf("%s renders %d replicas at the local profile; the laptop profile is "+
				"single-replica everything", nameOf(d), n)
		}
	}
}

// The local profile is enforced by the template, not merely defaulted in a
// values file. A profile that a stray --set can override is a suggestion, and
// this one exists because a laptop cluster cannot schedule what prod asks for.
func TestLocalProfileIsEnforcedNotDefaulted(t *testing.T) {
	docs := render(t, "--set", "profile=local", "--set", "operator.replicas=5")
	for _, d := range kindsOf(docs, "Deployment") {
		if n := replicasOf(t, d); n != 1 {
			t.Errorf("%s rendered %d replicas at profile=local with replicas=5 overridden. "+
				"The profile must win: on a single-node cluster the extra pods would sit "+
				"Pending and the install would look broken for a reason nobody could see.",
				nameOf(d), n)
		}
	}
}

func replicasOf(t *testing.T, d map[string]any) int {
	t.Helper()
	spec := dig(d, "spec")
	if spec == nil {
		return 1
	}
	switch v := spec["replicas"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case nil:
		return 1 // the Kubernetes default
	default:
		t.Fatalf("%s has a non-numeric replicas: %T", nameOf(d), v)
		return 0
	}
}

func nameOf(d map[string]any) string { return toStr(dig(d, "metadata")["name"]) }

func dig(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		cur = next
	}
	return cur
}

func toStr(v any) string {
	s, _ := v.(string)
	return s
}

func toStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, toStr(x))
	}
	return out
}

// The chart's copy of the operator rules must match what controller-gen
// produces from the kubebuilder markers. Hand-maintained RBAC in a chart is
// exactly what let leader election default on while the role granted no leases.
func TestChartRBACMatchesGeneratedRules(t *testing.T) {
	vendored, err := os.ReadFile(filepath.Join(chartPath, "files", "operator-rules.yaml"))
	if err != nil {
		t.Fatalf("the chart vendors no operator rules: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	if err != nil {
		t.Fatalf("read generated role: %v", err)
	}
	idx := strings.Index(string(generated), "rules:")
	if idx < 0 {
		t.Fatal("the generated role has no rules block")
	}
	if string(vendored) != string(generated)[idx:] {
		t.Error("charts/assayd/files/operator-rules.yaml has drifted from config/rbac/role.yaml. " +
			"The chart would grant permissions that do not match the markers in the code — " +
			"re-copy it in the same change that regenerates the role.")
	}
}

// tier: plus is not implemented. Rendering it as if it worked would be worse
// than refusing, so the chart refuses.
func TestUnimplementedTierIsRefusedNotIgnored(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatal("helm is required")
	}
	out, err := exec.Command("helm", "template", "assayd", chartPath, "--set", "tier=plus").CombinedOutput()
	if err == nil {
		t.Error("tier: plus rendered successfully, so an operator would believe they had " +
			"installed Argo Workflows, Phoenix, OpenFGA and eval runners. None exist.")
	}
	if !strings.Contains(string(out), "not implemented") {
		t.Errorf("the refusal does not say why: %s", out)
	}
}

// A floating tag makes a rollout irreproducible, which is the opposite of what
// a platform built on content-addressed revisions is for.
func TestFloatingImageTagIsRefused(t *testing.T) {
	out, err := exec.Command("helm", "template", "assayd", chartPath,
		"--set", "operator.image.tag=latest").CombinedOutput()
	if err == nil {
		t.Error("operator.image.tag=latest rendered successfully")
	}
	if !strings.Contains(string(out), "Pin a version") {
		t.Errorf("the refusal does not say what to do instead: %s", out)
	}
}

// The prod profile must render what it claims. values.yaml described prod as
// "replicas, anti-affinity, PDBs" while rendering a single unprotected pod —
// the same defect as a flag whose help text names a bound the code does not
// enforce, which is worse than saying nothing.
func TestProdProfileRendersWhatItClaims(t *testing.T) {
	docs := render(t) // prod is the default

	deploys := kindsOf(docs, "Deployment")
	if len(deploys) == 0 {
		t.Fatal("no Deployment rendered")
	}
	for _, d := range deploys {
		if n := replicasOf(t, d); n < 2 {
			t.Errorf("%s renders %d replica(s) at the prod profile. The operator is "+
				"leader-elected, so a standby takes over on node failure instead of waiting "+
				"for a reschedule — one replica means an outage lasts as long as scheduling does.",
				nameOf(d), n)
		}
		spec := dig(d, "spec", "template", "spec")
		if len(dig(spec, "affinity")) == 0 && spec["topologySpreadConstraints"] == nil {
			t.Errorf("%s has neither anti-affinity nor topology spread at prod: both replicas "+
				"can land on one node, so the standby dies with the active one", nameOf(d))
		}
	}

	if len(kindsOf(docs, "PodDisruptionBudget")) == 0 {
		t.Error("no PodDisruptionBudget at the prod profile: a node drain can take every " +
			"operator replica at once, and nothing reconciles until they reschedule")
	}
}

// A PDB that cannot be satisfied is worse than none: it blocks drains forever
// and turns routine node maintenance into an incident.
func TestPodDisruptionBudgetPermitsDrains(t *testing.T) {
	docs := render(t)
	for _, pdb := range kindsOf(docs, "PodDisruptionBudget") {
		spec := dig(pdb, "spec")
		if spec["minAvailable"] != nil && spec["maxUnavailable"] != nil {
			t.Errorf("%s sets both minAvailable and maxUnavailable, which is rejected", nameOf(pdb))
		}
		// With N replicas, minAvailable: N permits no disruption at all.
		if mn, ok := spec["minAvailable"]; ok && mn != nil {
			for _, d := range kindsOf(docs, "Deployment") {
				if toNum(mn) >= replicasOf(t, d) {
					t.Errorf("%s requires minAvailable=%v against %d replicas: no pod can ever "+
						"be evicted, so `kubectl drain` hangs and node maintenance becomes an "+
						"incident", nameOf(pdb), mn, replicasOf(t, d))
				}
			}
		}
	}
}

// The local profile must NOT render a PDB. On a single-node laptop cluster a
// disruption budget can only block things.
func TestLocalProfileHasNoDisruptionBudget(t *testing.T) {
	docs := render(t, "--set", "profile=local")
	if n := len(kindsOf(docs, "PodDisruptionBudget")); n != 0 {
		t.Errorf("the local profile renders %d PodDisruptionBudget(s). On a single-node "+
			"cluster that only blocks drains.", n)
	}
}

func toNum(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// A digest pins content; a tag pins a name that can be moved to point at other
// content after it was signed. assayd's own admission rejects agent images that
// are not digest-pinned, so the chart has to be able to express the same thing
// about the operator.
//
// It does NOT reject unsigned ones: this comment used to say "digest-pinned and
// cosign-signed (ADR-0019)", and nothing in this repository verifies any
// signature — CEL cannot, and the chart ships no policy that does. ADR-0019
// states the requirement; design 07 A2's Sigstore policy-controller binding is
// what would enforce it, and digest-pinning is its precondition rather than a
// weaker version of it.
func TestChartSupportsDigestPinning(t *testing.T) {
	const digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	docs := render(t, "--set", "operator.image.digest="+digest)

	for _, d := range kindsOf(docs, "Deployment") {
		spec := dig(d, "spec", "template", "spec")
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			img := toStr(dig2(c)["image"])
			if !strings.Contains(img, "@"+digest) {
				t.Errorf("%s renders image %q with a digest set. A tag can be repointed "+
					"after signing; only a digest names the content that was signed.",
					nameOf(d), img)
			}
			if strings.Contains(img, ":") && strings.Contains(img, "@") {
				// repo:tag@digest is legal but the tag is then decorative and
				// misleading — the digest wins, silently.
				before, _, _ := strings.Cut(img, "@")
				if strings.Count(before, ":") > 0 {
					t.Errorf("%s renders %q, carrying both a tag and a digest. The digest "+
						"wins, so the tag is decorative and reads as though it mattered.",
						nameOf(d), img)
				}
			}
		}
	}
}

// The default values must not point at an image that does not exist. An
// aspirational default is a `helm install` that fails with ImagePullBackOff and
// no explanation of why.
func TestDefaultImageIsPublishable(t *testing.T) {
	docs := render(t)
	for _, d := range kindsOf(docs, "Deployment") {
		spec := dig(d, "spec", "template", "spec")
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			img := toStr(dig2(c)["image"])
			if !strings.HasPrefix(img, "ghcr.io/") {
				t.Errorf("%s renders image %q, which is not in the registry the release "+
					"workflow publishes to", nameOf(d), img)
			}
		}
	}
}

func dig2(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// OCI repository names must be lowercase. The organization is "Quinyte", so any
// path interpolated from github.repository_owner produces an invalid tag —
// which is how the first v0.1.1 release failed, after the build had already run.
// Certificate identities are GitHub URLs and keep their original case, so this
// checks image references specifically rather than every mention of the org.
func TestRegistryPathsAreLowercase(t *testing.T) {
	docs := render(t)
	for _, d := range kindsOf(docs, "Deployment") {
		spec := dig(d, "spec", "template", "spec")
		containers, _ := spec["containers"].([]any)
		for _, c := range containers {
			img := toStr(dig2(c)["image"])
			repo, _, _ := strings.Cut(img, "@")
			repo, _, _ = strings.Cut(repo, ":")
			if repo != strings.ToLower(repo) {
				t.Errorf("%s renders image %q: an OCI repository name must be lowercase, "+
					"so this is rejected at push time — after the build has already run",
					nameOf(d), img)
			}
		}
	}
}

// Design 07 A5.9: the chart reserves the assayd.dev namespace labels to the
// operator identity, because SPIRE and the Gateway act on those labels and
// neither reads the operator's binding record. The operator fail-closes when
// the policies are absent, so a chart that dropped them would take every Agent
// down with RunNamespaceUnavailable=LabelAuthorityAbsent.
func TestChartShipsTheLabelReservingAdmissionPolicies(t *testing.T) {
	docs := render(t)
	policies := map[string]map[string]any{}
	for _, d := range kindsOf(docs, "ValidatingAdmissionPolicy") {
		policies[nameOf(d)] = d
	}
	bindings := map[string]bool{}
	for _, d := range kindsOf(docs, "ValidatingAdmissionPolicyBinding") {
		bindings[nameOf(d)] = true
	}
	for _, name := range []string{"assayd-namespace-labels", "assayd-gateway-routes"} {
		if policies[name] == nil {
			t.Errorf("the chart renders no ValidatingAdmissionPolicy %s; the operator refuses to create "+
				"run namespaces without it", name)
			continue
		}
		if !bindings[name] {
			t.Errorf("policy %s has no binding, so it validates nothing", name)
		}
		spec, _ := policies[name]["spec"].(map[string]any)
		if spec["failurePolicy"] != "Fail" {
			t.Errorf("policy %s has failurePolicy %v; an unavailable admission chain must deny, not admit", name, spec["failurePolicy"])
		}
	}
	// The permitted identities are rendered INTO the CEL — no params object,
	// so nothing that can go missing turns the policy into a cluster-wide
	// denial of namespace writes — and at core tier the list is exactly the
	// operator's ServiceAccount: every extra identity is a principal that can
	// mint a SPIFFE-selected namespace.
	for _, d := range kindsOf(docs, "ConfigMap") {
		if nameOf(d) == "assayd-operators" {
			t.Error("the chart renders a assayd-operators params ConfigMap; deleting it would deny every " +
				"namespace write in the cluster")
		}
	}
	for _, name := range []string{"assayd-namespace-labels", "assayd-gateway-routes"} {
		spec, _ := policies[name]["spec"].(map[string]any)
		if _, has := spec["paramKind"]; has {
			t.Errorf("policy %s declares a paramKind; identities must be inline", name)
		}
		want := `request.userInfo.username in ["system:serviceaccount:assayd-system:assayd-agent-operator"]`
		found := false
		for _, v := range toList(spec["variables"]) {
			vm, _ := v.(map[string]any)
			if vm["name"] == "operator" && vm["expression"] == want {
				found = true
			}
		}
		if !found {
			t.Errorf("policy %s does not permit exactly the operator's ServiceAccount: want variable "+
				"operator = %s", name, want)
		}
	}
	// Labels can be written through the status and finalize subresources too.
	spec, _ := policies["assayd-namespace-labels"]["spec"].(map[string]any)
	rules := toList(dig(spec, "matchConstraints")["resourceRules"])
	var resources []string
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		resources = append(resources, toStrings(rm["resources"])...)
	}
	for _, want := range []string{"namespaces", "namespaces/status", "namespaces/finalize"} {
		if !contains(resources, want) {
			t.Errorf("the namespace-label policy does not match %s, so a label written through it slips past: %v", want, resources)
		}
	}
	// A parentRef without a namespace refers to the route's own namespace, so
	// the route policy must resolve it that way or a bare-name ref from inside
	// the Gateway's namespace bypasses it. `variable` fails when the variable
	// is gone, so a rename cannot turn this into a loop that matches nothing.
	for _, v := range []string{"attached", "wasAttached"} {
		if got := variable(t, policies["assayd-gateway-routes"], v); !strings.Contains(got, "request.namespace") {
			t.Errorf("%s does not resolve a bare-name parentRef to the route's namespace: %s", v, got)
		}
	}
}

func toList(v any) []any { l, _ := v.([]any); return l }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// The operator is told where it runs through the downward API, so the binding
// records (design 02 A60) land where the Pod actually is.
func TestOperatorIsToldItsNamespace(t *testing.T) {
	docs := render(t)
	deps := kindsOf(docs, "Deployment")
	if len(deps) != 1 {
		t.Fatalf("want one Deployment, got %d", len(deps))
	}
	containers, _ := dig(dig(dig(deps[0], "spec"), "template"), "spec")["containers"].([]any)
	if len(containers) == 0 {
		t.Fatal("no containers")
	}
	c, _ := containers[0].(map[string]any)
	args := toStrings(c["args"])
	found := false
	for _, a := range args {
		if a == "--operator-namespace=$(POD_NAMESPACE)" {
			found = true
		}
	}
	if !found {
		t.Errorf("the operator is not passed --operator-namespace from the downward API: %v", args)
	}
}

// operatorArgs is the rendered operator container's args, for the tests that
// pin what the chart tells the binary.
func operatorArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	deps := kindsOf(render(t, extra...), "Deployment")
	if len(deps) != 1 {
		t.Fatalf("want one Deployment, got %d", len(deps))
	}
	containers, _ := dig(dig(dig(deps[0], "spec"), "template"), "spec")["containers"].([]any)
	if len(containers) == 0 {
		t.Fatal("no containers")
	}
	c, _ := containers[0].(map[string]any)
	return toStrings(c["args"])
}

// The gateway values must REACH the binary, and this is the cheap gate for it.
//
// Design 07 A6.10 records a mutation — rendering `--gateway-enabled=false`
// regardless of the value — that only the k3d e2e lane caught. An e2e lane is
// the expensive gate and the one a contributor does not run; a value that
// silently stops reaching the operator would ship green through `make test` and
// through the kind lane, where `gateway.enabled` is false and the argument's
// value is unobservable.
func TestTheGatewayValuesReachTheOperator(t *testing.T) {
	off := operatorArgs(t)
	if !contains(off, "--gateway-enabled=false") {
		t.Errorf("the default install does not tell the operator the gateway is off: %v. P1 ships "+
			"`gateway.enabled: false` and design 03 §3.1 makes that a declared tier, not an "+
			"absence — the operator has to be TOLD, or it decides by its own default.", off)
	}

	on := operatorArgs(t, "--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080",
		"--set", "gateway.namespace=somewhere-else",
		"--set", "gateway.name=gw", "--set", "gateway.hostnameSuffix=example.test")
	for _, want := range []string{
		"--gateway-enabled=true",
		"--gateway-name=gw",
		"--gateway-namespace=somewhere-else",
		"--gateway-hostname-suffix=example.test",
	} {
		if !contains(on, want) {
			t.Errorf("the operator is not passed %s: %v", want, on)
		}
	}
	// THE regression this test exists for. Design 07 A6.2 measured the route
	// reservation failing open — silently, in the permissive direction — because
	// the Gateway's namespace was conflated with the operator's. The route the
	// operator authors and the CEL that reserves route authorship must name one
	// namespace, so a `gateway.namespace` that quietly fell back to
	// `.Values.namespace` while admission.yaml's $gwNS did not is the same
	// failure from the other end.
	if contains(on, "--gateway-namespace=assayd-system") {
		t.Error("gateway.namespace was set to somewhere-else and the operator was told " +
			"assayd-system. The Gateway's namespace is not the operator's: A6.2 measured an " +
			"ordinary identity attaching a route when those two were conflated.")
	}
}

// gateway.url reaches the operator as --gateway-url, which it injects into every
// agent as ASSAYD_GATEWAY_URL — the egress base URL an agent's tool calls must
// traverse. Unset renders no flag at all, so an install with no Gateway injects
// nothing rather than an empty variable an agent might dial.
func TestTheGatewayURLReachesTheOperatorOnlyWhenSet(t *testing.T) {
	if args := operatorArgs(t); containsPrefix(args, "--gateway-url") {
		t.Errorf("the default install passes %v; with no gateway.url nothing may be injected", args)
	}
	args := operatorArgs(t, "--set", "gateway.url=http://gw.example:8081")
	if !contains(args, "--gateway-url=http://gw.example:8081") {
		t.Errorf("gateway.url did not reach the operator: %v", args)
	}
}

func containsPrefix(xs []string, prefix string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

// And the fallback, which admission.yaml's `$gwNS` performs identically. A
// single-namespace install must be unchanged, so an unset value means the
// namespace the OPERATOR runs in — not the Helm release namespace, which
// hack/e2e.sh proves is a different thing by installing into `default`.
func TestAnUnsetGatewayNamespaceFallsBackToTheOperators(t *testing.T) {
	args := operatorArgs(t, "--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080",
		"--set", "namespace=assayd-elsewhere")
	if !contains(args, "--gateway-namespace=assayd-elsewhere") {
		t.Errorf("an unset gateway.namespace did not fall back to .Values.namespace: %v. "+
			"admission.yaml renders `$gwNS` with the same `| default .Values.namespace`, and if "+
			"the two disagree the reservation guards a namespace no route names.", args)
	}
}

// Every rendered document carries apiVersion and kind. `helm template` does
// not validate that and `helm upgrade` does: a Helm whitespace trim once glued
// a policy's apiVersion onto the comment line above it, so `helm template`
// rendered a document the test suite still found by kind and the install
// failed with "apiVersion not set".
func TestEveryRenderedDocumentHasAnAPIVersion(t *testing.T) {
	for _, d := range render(t, "-f", filepath.Join(chartPath, "values-local.yaml")) {
		if d["apiVersion"] == nil || d["kind"] == nil {
			t.Errorf("a rendered document has no apiVersion or kind: %v", nameOf(d))
		}
	}
}
